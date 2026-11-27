package auth

import (
	"crypto"
	"crypto/x509"
	"encoding/xml"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/crewjam/saml"
	dsig "github.com/russellhaering/goxmldsig"

	"github.com/nitpick-oss/nitpick/internal/server"
)

// SAMLConfig is one SAML 2.0 identity provider.
type SAMLConfig struct {
	// IDPMetadata is the provider's metadata: where to send requests and the
	// certificates its assertions are signed with. Those certificates are the
	// only thing that makes an assertion believable, so they come from the
	// operator's configuration and not from the assertion.
	IDPMetadata *saml.EntityDescriptor

	// EntityID is this server's name to the provider, and the audience the
	// assertions must be for. ACSURL is where the provider posts them, and the
	// destination they must name.
	EntityID string
	ACSURL   *url.URL

	// Key and Certificate are this server's own, used to sign requests when the
	// provider wants that and published in the metadata.
	Key         crypto.Signer
	Certificate *x509.Certificate

	// GroupsAttribute is the assertion attribute that lists the person's groups,
	// for the role rules. Empty means "groups".
	GroupsAttribute string
	Label           string

	// Clock is for tests. Nil is the wall clock.
	Clock *dsig.Clock
}

// SAML signs people in with a SAML 2.0 provider using the HTTP-Redirect request
// binding and the HTTP-POST response binding.
//
// A service provider is attacked with an unsigned assertion, with a signature
// that covers one element while the application reads another ("wrapping"), and
// with a genuine assertion played a second time. The library refuses the first
// two and the tests show it. The third is this type's job, done with a table of
// the assertion IDs it has accepted.
type SAML struct {
	cfg   SAMLConfig
	sp    *saml.ServiceProvider
	flows *flows
	seen  *seenAssertions
	log   *slog.Logger
	label string
}

const samlCookie = "__Host-nitpick_saml"

// NewSAML builds the service provider. It fetches nothing. The provider's
// metadata is given, so start-up needs no network and no metadata URL can swap in
// a signing certificate.
func NewSAML(cfg SAMLConfig, log *slog.Logger) (*SAML, error) {
	if cfg.IDPMetadata == nil || cfg.EntityID == "" || cfg.ACSURL == nil {
		return nil, errors.New("saml needs the provider's metadata, an entity id and an ACS URL")
	}
	if cfg.ACSURL.Scheme != "https" {
		return nil, errors.New("the SAML ACS URL must be https")
	}
	if cfg.Key == nil || cfg.Certificate == nil {
		return nil, errors.New("saml needs this server's key and certificate")
	}
	if len(cfg.IDPMetadata.IDPSSODescriptors) == 0 {
		return nil, errors.New("the SAML metadata describes no identity provider")
	}
	if log == nil {
		log = slog.Default()
	}
	if cfg.GroupsAttribute == "" {
		cfg.GroupsAttribute = "groups"
	}
	label := cfg.Label
	if label == "" {
		label = "SAML"
	}
	sp := &saml.ServiceProvider{
		EntityID:    cfg.EntityID,
		Key:         cfg.Key,
		Certificate: cfg.Certificate,
		AcsURL:      *cfg.ACSURL,
		IDPMetadata: cfg.IDPMetadata,
		Clock:       cfg.Clock,
		// No unsolicited responses: every assertion answers a request this server
		// made, so it carries an InResponseTo that is in the table.
		AllowIDPInitiated: false,
	}
	return &SAML{cfg: cfg, sp: sp, flows: newFlows(10_000), seen: newSeenAssertions(), log: log, label: label}, nil
}

// ID implements server.Provider.
func (s *SAML) ID() string { return "saml" }

// Label implements server.Provider.
func (s *SAML) Label() string { return s.label }

// Form implements server.Provider.
func (s *SAML) Form() server.LoginForm { return server.FormRedirect }

// CrossSitePosts implements server.Provider. The provider's page posts to the
// assertion consumer, so there is no CSRF token to carry. A request ID in the
// table and a cookie tie the answer to a login this server started.
func (s *SAML) CrossSitePosts() []string { return []string{"/auth/saml/acs"} }

// Mount implements server.Provider.
func (s *SAML) Mount(mux *http.ServeMux, host server.Host) {
	mux.HandleFunc("GET /auth/saml/login", s.login)
	mux.HandleFunc("POST /auth/saml/acs", func(w http.ResponseWriter, r *http.Request) { s.acs(w, r, host) })
	mux.HandleFunc("GET /auth/saml/metadata", s.metadata)
}

func (s *SAML) metadata(w http.ResponseWriter, r *http.Request) {
	buf, err := xml.MarshalIndent(s.sp.Metadata(), "", "  ")
	if err != nil {
		http.Error(w, "could not produce metadata", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/samlmetadata+xml")
	_, _ = w.Write(buf)
}

func (s *SAML) login(w http.ResponseWriter, r *http.Request) {
	sso := s.sp.GetSSOBindingLocation(saml.HTTPRedirectBinding)
	if sso == "" {
		http.Error(w, "the provider offers no redirect binding", http.StatusInternalServerError)
		return
	}
	req, err := s.sp.MakeAuthenticationRequest(sso, saml.HTTPRedirectBinding, saml.HTTPPostBinding)
	if err != nil {
		s.log.Error("saml authn request", "error", err)
		http.Error(w, "could not start the sign-in", http.StatusInternalServerError)
		return
	}
	// SameSite=None, because the provider posts the answer from its own page and a
	// Lax cookie does not travel on a cross-site POST. It holds a random value
	// and nothing else.
	binding, err := bindCookie(w, samlCookie, "/auth/saml/", http.SameSiteNoneMode)
	if err != nil {
		http.Error(w, "could not start the sign-in", http.StatusInternalServerError)
		return
	}
	relay, err := s.flows.put(&flow{binding: binding, secret: req.ID, next: r.URL.Query().Get("next")})
	if err != nil {
		http.Error(w, "could not start the sign-in", http.StatusInternalServerError)
		return
	}
	target, err := req.Redirect(relay, s.sp)
	if err != nil {
		s.log.Error("saml redirect", "error", err)
		http.Error(w, "could not start the sign-in", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, target.String(), http.StatusFound)
}

func (s *SAML) acs(w http.ResponseWriter, r *http.Request, host server.Host) {
	if err := r.ParseForm(); err != nil {
		s.fail(w, r, host, "the form could not be read")
		return
	}
	// The relay state names the request this answers. It is taken before any check,
	// so an answer uses its request up whatever happens next.
	fl, ok := s.flows.take(r.PostFormValue("RelayState"))
	if !ok {
		s.fail(w, r, host, "unknown or expired relay state: no sign-in of ours is waiting for this answer")
		return
	}
	if !bound(r, samlCookie, fl.binding) {
		s.fail(w, r, host, "the answer arrived in a browser that did not start the sign-in")
		return
	}
	assertion, err := s.sp.ParseResponse(r, []string{fl.secret})
	if err != nil {
		reason := "the response did not validate"
		var invalid *saml.InvalidResponseError
		if errors.As(err, &invalid) {
			reason += ": " + invalid.PrivateErr.Error()
		} else {
			reason += ": " + err.Error()
		}
		s.fail(w, r, host, reason)
		return
	}
	// The signature proves the provider said it. Nothing in it stops someone who
	// captured the assertion from saying it again, so an ID is good once.
	if assertion.ID == "" || !s.seen.firstTime(assertion.ID, assertionExpiry(assertion)) {
		s.fail(w, r, host, "this assertion was already used or has no ID")
		return
	}
	if assertion.Subject == nil || assertion.Subject.NameID == nil || assertion.Subject.NameID.Value == "" {
		s.fail(w, r, host, "the assertion has no subject")
		return
	}
	host.SignIn(w, r, server.Identity{
		Provider: s.ID(),
		Subject:  assertion.Issuer.Value + "#" + assertion.Subject.NameID.Value,
		Name:     attribute(assertion, "displayName", "name", "cn"),
		Groups:   attributeValues(assertion, s.cfg.GroupsAttribute),
	}, fl.next)
}

func (s *SAML) fail(w http.ResponseWriter, r *http.Request, host server.Host, reason string) {
	s.log.Warn("saml sign-in refused", "reason", reason)
	host.LoginFailed(w, r, http.StatusUnauthorized, "Sign-in with "+s.label+" did not complete. Try again.")
}

// assertionExpiry is how long an ID must be remembered: until the assertion could
// no longer be accepted anyway, plus the clock skew the library allows. An ID
// forgotten sooner could be replayed in the gap.
func assertionExpiry(a *saml.Assertion) time.Time {
	latest := time.Now().Add(time.Hour)
	if a.Conditions != nil && !a.Conditions.NotOnOrAfter.IsZero() {
		latest = a.Conditions.NotOnOrAfter
	}
	if a.Subject != nil {
		for _, c := range a.Subject.SubjectConfirmations {
			if c.SubjectConfirmationData != nil && c.SubjectConfirmationData.NotOnOrAfter.After(latest) {
				latest = c.SubjectConfirmationData.NotOnOrAfter
			}
		}
	}
	return latest.Add(2 * saml.MaxClockSkew)
}

func attribute(a *saml.Assertion, names ...string) string {
	for _, n := range names {
		if v := attributeValues(a, n); len(v) > 0 {
			return v[0]
		}
	}
	return ""
}

func attributeValues(a *saml.Assertion, name string) []string {
	var out []string
	for _, st := range a.AttributeStatements {
		for _, attr := range st.Attributes {
			if attr.Name != name && attr.FriendlyName != name {
				continue
			}
			for _, v := range attr.Values {
				if v.Value != "" {
					out = append(out, v.Value)
				}
			}
		}
	}
	return out
}

// seenAssertions remembers the IDs of assertions that signed someone in, until
// they would have expired anyway.
type seenAssertions struct {
	mu  sync.Mutex
	ids map[string]time.Time
	now func() time.Time
}

func newSeenAssertions() *seenAssertions { return &seenAssertions{ids: map[string]time.Time{}} }

func (s *seenAssertions) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// firstTime records an ID and reports whether it had not been seen. An ID that
// was seen and has expired may be seen again: its assertion cannot validate any
// more, so the question does not arise.
func (s *seenAssertions) firstTime(id string, until time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock()
	for k, exp := range s.ids {
		if now.After(exp) {
			delete(s.ids, k)
		}
	}
	if _, dup := s.ids[id]; dup {
		return false
	}
	s.ids[id] = until
	return true
}

func (s *seenAssertions) len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.ids)
}
