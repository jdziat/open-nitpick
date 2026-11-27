package auth

import (
	"io"
	"time"

	"github.com/beevik/etree"
	"github.com/crewjam/saml"

	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/nitpick-oss/nitpick/internal/server"
)

type samlSite struct {
	*site
	idp  *samlIdP
	s    *SAML
	acs  string
	spID string
	log  *strings.Builder
}

func newSAMLSite(t *testing.T, idp *samlIdP, rules ...server.RoleRule) *samlSite {
	t.Helper()
	var handler http.Handler
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { handler.ServeHTTP(w, r) }))
	t.Cleanup(ts.Close)
	acsURL, _ := url.Parse(ts.URL + "/auth/saml/acs")
	spKey, spCert := selfSigned(t, "sp.example")
	log := &strings.Builder{}
	s, err := NewSAML(SAMLConfig{
		IDPMetadata: idp.metadata(), EntityID: ts.URL + "/auth/saml/metadata", ACSURL: acsURL, Key: spKey, Certificate: spCert,
	}, slog.New(slog.NewTextHandler(log, nil)))
	if err != nil {
		t.Fatal(err)
	}
	roles, err := server.NewRoleMap(rules)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := server.New(server.Config{
		Components: []server.Component{echo{}}, Providers: []server.Provider{s}, Roles: roles, Assets: builtUI(),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	handler = srv
	jar, _ := cookiejar.New(nil)
	c := ts.Client()
	c.Jar = jar
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &samlSite{site: &site{t: t, ts: ts, c: c, srv: srv}, idp: idp, s: s, acs: acsURL.String(), spID: ts.URL + "/auth/saml/metadata", log: log}
}

// start sends the browser to the provider and returns the request ID the
// provider sees and the relay state it must return.
func (s *samlSite) start(next string) (id, relay string) {
	s.t.Helper()
	resp, err := s.c.Get(s.ts.URL + "/auth/saml/login?next=" + url.QueryEscape(next))
	if err != nil {
		s.t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		s.t.Fatalf("login = %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, s.idp.ssoURL) {
		s.t.Fatalf("the browser was sent to %q, not the provider", loc)
	}
	u, _ := url.Parse(loc)
	return requestID(s.t, loc), u.Query().Get("RelayState")
}

// post is the provider's page posting an answer to the assertion consumer.
func (s *samlSite) post(samlResponse, relay string, hdr map[string]string) (reply, string) {
	s.t.Helper()
	form := url.Values{"SAMLResponse": {samlResponse}, "RelayState": {relay}}
	req, _ := http.NewRequest("POST", s.acs, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := s.c.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return replyOf(resp), string(b)
}

func (s *samlSite) signedIn() bool {
	resp, err := s.c.Get(s.ts.URL + "/v1/echo/who")
	if err != nil {
		s.t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// ok is a full, valid sign-in.
func (s *samlSite) ok(next string) reply {
	s.t.Helper()
	id, relay := s.start(next)
	resp, body := s.post(s.idp.response(s.acs, s.spID, id), relay, nil)
	if resp.StatusCode != http.StatusSeeOther {
		s.t.Fatalf("a valid sign-in = %d: %s\nlog: %s", resp.StatusCode, body, s.log.String())
	}
	return resp
}

func TestASAMLSignInSucceedsAndMapsGroupsToRoles(t *testing.T) {
	idp := newSAMLIdP(t)
	idp.groups = []string{"staff", "ops"}
	s := newSAMLSite(t, idp, server.RoleRule{Roles: []server.Role{"admin"}, Group: "ops"})
	resp := s.ok("/ui/somewhere")
	if resp.Header.Get("Location") != "/ui/somewhere" {
		t.Errorf("sign-in went to %q, want the requested page", resp.Header.Get("Location"))
	}
	id := lastIdentity(t, s.site)
	if id.Provider != "saml" || id.Subject != idp.entityID+"#una@example.com" || id.Name != "Una Person" {
		t.Errorf("identity = %+v", id)
	}
	if !id.Has("admin") {
		t.Errorf("a member of ops holds %v, want admin", id.Roles)
	}
}

// refused runs a sign-in whose response is made wrong by mutate, and checks that
// no session came out of it and what the person was told does not carry the
// reason. The reason is in the log.
func samlRefused(t *testing.T, name string, mutate func(*samlIdP)) *samlSite {
	t.Helper()
	idp := newSAMLIdP(t)
	mutate(idp)
	s := newSAMLSite(t, idp)
	id, relay := s.start("/ui/")
	resp, body := s.post(idp.response(s.acs, s.spID, id), relay, nil)
	if resp.StatusCode == http.StatusSeeOther {
		t.Errorf("%s: the response signed someone in", name)
	}
	if s.signedIn() {
		t.Errorf("%s: a session exists afterwards", name)
	}
	for _, leak := range []string{"signature", "audience", "Audience", "assertion", "digest", "InResponseTo", idp.entityID} {
		if strings.Contains(body, leak) {
			t.Errorf("%s: what the person is told shows %q", name, leak)
		}
	}
	if !strings.Contains(s.log.String(), "saml sign-in refused") {
		t.Errorf("%s: the refusal was not logged:\n%s", name, s.log.String())
	}
	return s
}

// Rule 9: an assertion nobody signed is refused. Rule 10: the same flow with the
// signature is not (TestASAMLSignInSucceeds above).
func TestAnUnsignedSAMLAssertionIsRefused(t *testing.T) {
	s := samlRefused(t, "unsigned", func(p *samlIdP) { p.signAssertion = false; p.signResponse = false })
	if !strings.Contains(s.log.String(), "did not validate") {
		t.Errorf("the refusal was not for validation:\n%s", s.log.String())
	}
}

// A signature that was valid and has been broken by changing what it covers.
func TestASAMLAssertionChangedAfterSigningIsRefused(t *testing.T) {
	samlRefused(t, "tampered", func(p *samlIdP) { p.tamperName = "admin@example.com" })
}

// A valid signature by a certificate the operator did not give us is a
// signature by a stranger.
func TestASAMLAssertionSignedByAnUnknownCertificateIsRefused(t *testing.T) {
	samlRefused(t, "a stranger's key", func(p *samlIdP) {
		key, cert := selfSigned(t, "evil.example")
		p.signWith, p.signedBy = key, cert
	})
	// The same stranger cannot borrow the real certificate: its public half is
	// public, and the signature has to be made with the private half.
	samlRefused(t, "the real certificate, the wrong key", func(p *samlIdP) {
		key, _ := selfSigned(t, "evil.example")
		p.signWith = key
	})
}

func TestASAMLAssertionForAnotherAudienceIsRefused(t *testing.T) {
	samlRefused(t, "another audience", func(p *samlIdP) { p.audience = "https://someone-else.example/metadata" })
}

func TestASAMLAssertionForAnotherRecipientIsRefused(t *testing.T) {
	samlRefused(t, "another recipient", func(p *samlIdP) { p.recipient = "https://evil.example/acs" })
}

func TestASAMLResponseForAnotherDestinationIsRefused(t *testing.T) {
	samlRefused(t, "another destination", func(p *samlIdP) { p.dest = "https://evil.example/acs"; p.signResponse = true })
}

func TestAnExpiredSAMLAssertionIsRefused(t *testing.T) {
	samlRefused(t, "expired", func(p *samlIdP) { p.notBefore, p.notAfter = -time.Hour, -30*time.Minute })
}

func TestASAMLAssertionNotYetValidIsRefused(t *testing.T) {
	samlRefused(t, "not yet valid", func(p *samlIdP) { p.notBefore, p.notAfter = time.Hour, 2*time.Hour })
}

func TestASAMLAssertionFromAnotherIssuerIsRefused(t *testing.T) {
	samlRefused(t, "another issuer", func(p *samlIdP) { p.issuer = "https://evil.example/metadata" })
}

func TestAFailureStatusIsNotASignIn(t *testing.T) {
	samlRefused(t, "a failure status", func(p *samlIdP) { p.status = saml.StatusRequester })
}

func TestASAMLAssertionWithNoSubjectIsRefused(t *testing.T) {
	samlRefused(t, "no subject", func(p *samlIdP) { p.nameID = "" })
}

// Signature wrapping. A forged assertion for an administrator is placed where a
// careless reader would find it first, and the genuine signed assertion is
// tucked in beside it or inside it, so a signature still verifies. The result must
// be a refusal and never the forged identity. Each case also says what it was
// refused for: a refusal for malformed XML would prove nothing about the defence.
func TestASignatureWrappedAroundAForgedAssertionDoesNotSignInTheForgery(t *testing.T) {
	type attack struct {
		build func(p *samlIdP, acs, sp, id string) string
		why   string // part of the library's reason, from the log
	}
	forged := func(p *samlIdP, acs, sp, id string) *etree.Element {
		f := *p
		f.nameID = "admin@example.com"
		return parseEl(t, f.assertionXML(acs, sp, id))
	}
	genuine := func(p *samlIdP, acs, sp, id string) *etree.Element {
		return p.sign(parseEl(t, p.assertionXML(acs, sp, id)))
	}
	for name, a := range map[string]attack{
		"the genuine assertion inside the forged one's Advice": {func(p *samlIdP, acs, sp, id string) string {
			f := forged(p, acs, sp, id)
			adv := etree.NewElement("saml:Advice")
			adv.AddChild(genuine(p, acs, sp, id))
			f.AddChild(adv)
			return p.wrap(acs, id, f)
		}, "signature element not present"},
		"the forged assertion first, the genuine one second": {func(p *samlIdP, acs, sp, id string) string {
			return p.wrap(acs, id, forged(p, acs, sp, id), genuine(p, acs, sp, id))
		}, "must contain exactly one Assertion"},
		"the genuine assertion first, the forged one second": {func(p *samlIdP, acs, sp, id string) string {
			return p.wrap(acs, id, genuine(p, acs, sp, id), forged(p, acs, sp, id))
		}, "must contain exactly one Assertion"},
		"the signature copied onto the forged assertion": {func(p *samlIdP, acs, sp, id string) string {
			f := forged(p, acs, sp, id)
			f.AddChild(genuine(p, acs, sp, id).FindElement("./Signature").Copy())
			return p.wrap(acs, id, f)
		}, "Missing signature referencing the top-level element"},
	} {
		idp := newSAMLIdP(t)
		s := newSAMLSite(t, idp)
		id, relay := s.start("/ui/")
		resp, _ := s.post(a.build(idp, s.acs, s.spID, id), relay, nil)
		if resp.StatusCode == http.StatusSeeOther || s.signedIn() {
			who := ""
			if s.signedIn() {
				who = lastIdentity(t, s.site).Subject
			}
			t.Errorf("%s: signed in as %q", name, who)
		}
		if !strings.Contains(s.log.String(), a.why) {
			t.Errorf("%s: refused, but not for %q:\n%s", name, a.why, s.log.String())
		}
	}
	// Rule 10: the genuine assertion on its own signs in, so each refusal above is
	// the wrapping's, not a document the library cannot read.
	idp := newSAMLIdP(t)
	newSAMLSite(t, idp).ok("/ui/")
}

// Rule 9, replay: an assertion ID that has signed someone in is not accepted
// again. Two flows are both valid requests of ours; an identity provider that
// issues the same assertion ID for both (a bug, or an assertion captured and
// re-issued) is stopped by the table of IDs, which is the only thing that can.
func TestAReplayedSAMLAssertionIDIsRefused(t *testing.T) {
	idp := newSAMLIdP(t)
	idp.assertID = "_the-same-assertion"
	s := newSAMLSite(t, idp)
	s.ok("/ui/") // the first use, which is fine

	// A second browser, with a second flow of its own, answered with an assertion
	// that has the same ID. It validates in every other way.
	jar, _ := cookiejar.New(nil)
	s.c.Jar = jar
	id, relay := s.start("/ui/")
	resp, _ := s.post(idp.response(s.acs, s.spID, id), relay, nil)
	if resp.StatusCode == http.StatusSeeOther {
		t.Fatal("an assertion ID that had already signed someone in was accepted again")
	}
	if !strings.Contains(s.log.String(), "already used") {
		t.Errorf("the refusal was not for the replayed ID:\n%s", s.log.String())
	}
	// Rule 10: a different ID in the same position is accepted, so the refusal is
	// the ID's.
	idp.assertID = ""
	s.ok("/ui/")
}

// The same answer, posted twice to the same flow, signs in once.
func TestASAMLAnswerPostedTwiceSignsInOnce(t *testing.T) {
	idp := newSAMLIdP(t)
	s := newSAMLSite(t, idp)
	id, relay := s.start("/ui/")
	answer := idp.response(s.acs, s.spID, id)
	if resp, _ := s.post(answer, relay, nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("the first answer = %d", resp.StatusCode)
	}
	jar, _ := cookiejar.New(nil)
	s.c.Jar = jar
	if resp, _ := s.post(answer, relay, nil); resp.StatusCode == http.StatusSeeOther {
		t.Error("an answer posted a second time signed someone in")
	}
}

// Unsolicited (identity-provider-initiated) responses are not accepted: every
// answer must be for a request this server made.
func TestASAMLResponseForARequestWeDidNotMakeIsRefused(t *testing.T) {
	idp := newSAMLIdP(t)
	s := newSAMLSite(t, idp)
	s.start("/ui/") // so the browser has a binding cookie
	for name, relay := range map[string]string{"no relay state": "", "a made-up relay state": "made-up"} {
		resp, _ := s.post(idp.response(s.acs, s.spID, "_a-request-nobody-made"), relay, nil)
		if resp.StatusCode == http.StatusSeeOther {
			t.Errorf("%s: an unsolicited response signed someone in", name)
		}
	}
	// A response that answers a different request than the one the relay state is for.
	_, relay := s.start("/ui/")
	resp, _ := s.post(idp.response(s.acs, s.spID, "_some-other-request"), relay, nil)
	if resp.StatusCode == http.StatusSeeOther {
		t.Error("a response to another request ID signed someone in under this relay state")
	}
}

// The binding cookie: an answer arriving in a browser that did not start the
// sign-in is refused, which is what stops login CSRF. An attacker signs in as
// themselves and hands the victim the answer; the victim's browser has no cookie
// for that flow, or has one for a different flow, and in neither case is signed in.
func TestASAMLAnswerInAnotherBrowserIsRefused(t *testing.T) {
	idp := newSAMLIdP(t)
	s := newSAMLSite(t, idp)
	id, relay := s.start("/ui/") // the attacker's browser starts a flow
	answer := idp.response(s.acs, s.spID, id)

	// A victim with no binding cookie at all.
	attackerJar := s.c.Jar
	noCookie, _ := cookiejar.New(nil)
	s.c.Jar = noCookie
	if resp, _ := s.post(answer, relay, nil); resp.StatusCode == http.StatusSeeOther {
		t.Fatal("an answer in a browser with no binding cookie signed it in")
	}
	// The attempt spent the flow, so even the attacker's own browser cannot use it.
	s.c.Jar = attackerJar
	if resp, _ := s.post(answer, relay, nil); resp.StatusCode == http.StatusSeeOther {
		t.Error("the flow survived a failed attempt and was usable afterwards")
	}

	// A victim with a real binding cookie, for a flow of their own.
	id2, relay2 := s.start("/ui/") // the attacker's second flow
	attackerAnswer := idp.response(s.acs, s.spID, id2)
	victimJar, _ := cookiejar.New(nil)
	s.c.Jar = victimJar
	idV, relayV := s.start("/ui/") // the victim's own flow, which gives them a cookie
	if resp, _ := s.post(attackerAnswer, relay2, nil); resp.StatusCode == http.StatusSeeOther {
		t.Error("a browser holding a different flow's binding cookie finished another browser's flow")
	}
	// Rule 10: the victim's own flow completes in the victim's own browser, so the
	// refusals above are the binding's doing.
	if resp, _ := s.post(idp.response(s.acs, s.spID, idV), relayV, nil); resp.StatusCode != http.StatusSeeOther {
		t.Errorf("a browser finishing its own flow = %d, want a sign-in", resp.StatusCode)
	}
}

// The consumer is reached by a POST that another site (the identity provider)
// starts, so it is exempt from the CSRF check even for a signed-in person, and
// nothing else is.
func TestOnlyTheAssertionConsumerAcceptsACrossSitePost(t *testing.T) {
	idp := newSAMLIdP(t)
	s := newSAMLSite(t, idp)
	s.ok("/ui/") // signed in, with a real session cookie

	id, relay := s.start("/ui/")
	resp, _ := s.post(idp.response(s.acs, s.spID, id), relay, map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://idp.example"})
	if resp.StatusCode == http.StatusForbidden {
		t.Error("the identity provider's cross-site POST to the consumer was refused by the CSRF layer")
	}
	// Anything else with a cross-site header and a session is refused.
	req, _ := http.NewRequest("POST", s.ts.URL+"/auth/logout", strings.NewReader(""))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	r2, err := s.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = r2.Body.Close()
	if r2.StatusCode != http.StatusForbidden {
		t.Errorf("a cross-site POST to sign-out = %d, want 403", r2.StatusCode)
	}
}

func TestTheSAMLBindingCookieIsHttpOnlySecureAndScopedToTheProvider(t *testing.T) {
	idp := newSAMLIdP(t)
	s := newSAMLSite(t, idp)
	resp, err := s.c.Get(s.ts.URL + "/auth/saml/login")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	var bind *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == samlCookie {
			bind = c
		}
	}
	if bind == nil || !bind.HttpOnly || !bind.Secure || bind.Path != "/auth/saml/" || bind.SameSite != http.SameSiteNoneMode {
		t.Errorf("binding cookie = %+v, want HttpOnly, Secure, Path=/auth/saml/, SameSite=None (the answer is a cross-site POST)", bind)
	}
}

// The request carries what the provider needs and no more.
func TestTheSAMLRequestIsSentByRedirectWithARelayState(t *testing.T) {
	idp := newSAMLIdP(t)
	s := newSAMLSite(t, idp)
	id, relay := s.start("/ui/x")
	if !strings.HasPrefix(id, "id-") && !strings.HasPrefix(id, "_") {
		t.Errorf("request id = %q", id)
	}
	if len(relay) < 40 {
		t.Errorf("relay state %q is too short to be random", relay)
	}
}

// The SP metadata the operator hands the provider names this server's ACS.
func TestTheServiceProviderMetadataNamesTheConsumer(t *testing.T) {
	idp := newSAMLIdP(t)
	s := newSAMLSite(t, idp)
	resp, err := s.c.Get(s.ts.URL + "/auth/saml/metadata")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), s.acs) || !strings.Contains(string(b), s.spID) {
		t.Errorf("metadata = %d %s", resp.StatusCode, b)
	}
	if strings.Contains(string(b), "PRIVATE KEY") {
		t.Error("the metadata contains a private key")
	}
}

func TestSAMLConfigThatCouldNotWorkIsRefused(t *testing.T) {
	idp := newSAMLIdP(t)
	key, cert := selfSigned(t, "sp")
	acs, _ := url.Parse("https://sp.example/auth/saml/acs")
	http1, _ := url.Parse("http://sp.example/auth/saml/acs")
	good := SAMLConfig{IDPMetadata: idp.metadata(), EntityID: "sp", ACSURL: acs, Key: key, Certificate: cert}
	if _, err := NewSAML(good, nil); err != nil {
		t.Fatalf("a good config was refused: %v", err)
	}
	for name, mutate := range map[string]func(*SAMLConfig){
		"no metadata":       func(c *SAMLConfig) { c.IDPMetadata = nil },
		"no entity id":      func(c *SAMLConfig) { c.EntityID = "" },
		"no ACS URL":        func(c *SAMLConfig) { c.ACSURL = nil },
		"an http ACS URL":   func(c *SAMLConfig) { c.ACSURL = http1 },
		"no key":            func(c *SAMLConfig) { c.Key = nil },
		"no certificate":    func(c *SAMLConfig) { c.Certificate = nil },
		"no IdP descriptor": func(c *SAMLConfig) { c.IDPMetadata = &saml.EntityDescriptor{EntityID: "x"} },
	} {
		c := good
		mutate(&c)
		if _, err := NewSAML(c, nil); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestTheAssertionIDTableForgetsExpiredIDsAndRemembersLiveOnes(t *testing.T) {
	now := time.Now()
	s := newSeenAssertions()
	s.now = func() time.Time { return now }
	if !s.firstTime("a", now.Add(time.Hour)) {
		t.Fatal("a new ID was refused")
	}
	if s.firstTime("a", now.Add(time.Hour)) {
		t.Error("a repeated ID was accepted")
	}
	now = now.Add(2 * time.Hour)
	if !s.firstTime("b", now.Add(time.Hour)) {
		t.Error("an unrelated ID was refused")
	}
	if s.len() != 1 {
		t.Errorf("the table holds %d IDs after the first expired, want 1", s.len())
	}
}

// An identity-provider-initiated response has no InResponseTo: nothing of ours
// asked for it. Here the flow, the cookie and the relay state are all genuine, so
// the only thing wrong with the response is that it answers no request. That is
// the check that AllowIDPInitiated=false is, and nothing else here would catch it.
func TestASAMLResponseThatAnswersNoRequestIsRefusedEvenInAGenuineFlow(t *testing.T) {
	idp := newSAMLIdP(t)
	s := newSAMLSite(t, idp)
	_, relay := s.start("/ui/")
	resp, _ := s.post(idp.response(s.acs, s.spID, ""), relay, nil)
	if resp.StatusCode == http.StatusSeeOther {
		t.Fatal("a response with no InResponseTo signed someone in")
	}
	if !strings.Contains(s.log.String(), "did not validate") {
		t.Errorf("the refusal was not the response's validation:\n%s", s.log.String())
	}
}

// An assertion with no ID cannot be told apart from another, so it cannot be
// checked for replay and is not accepted.
func TestASAMLAssertionWithNoIDIsRefused(t *testing.T) {
	idp := newSAMLIdP(t)
	idp.emptyAssertID = true
	s := newSAMLSite(t, idp)
	id, relay := s.start("/ui/")
	resp, _ := s.post(idp.response(s.acs, s.spID, id), relay, nil)
	if resp.StatusCode == http.StatusSeeOther {
		t.Error("an assertion with no ID signed someone in")
	}
}
