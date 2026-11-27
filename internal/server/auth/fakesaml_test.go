package auth

import (
	"bytes"
	"compress/flate"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/beevik/etree"
	"github.com/crewjam/saml"
	dsig "github.com/russellhaering/goxmldsig"
)

// samlIDP is an identity provider that writes real, XML-signed SAML responses.
// Every field is one way to make a response wrong.
type samlIDP struct {
	t        *testing.T
	key      *rsa.PrivateKey
	cert     *x509.Certificate
	entityID string
	ssoURL   string

	// What the next response says.
	nameID        string
	groups        []string
	audience      string // "" means the service provider's entity ID
	recipient     string // "" means the ACS URL
	dest          string // "" means the ACS URL
	issuer        string // "" means this provider's entity ID
	assertID      string // "" means a fresh one
	notBefore     time.Duration
	notAfter      time.Duration
	status        string
	signWith      *rsa.PrivateKey
	signedBy      *x509.Certificate
	signAssertion bool
	signResponse  bool
	tamperName    string // changes the NameID after signing
	emptyAssertID bool   // an assertion with an empty ID attribute
}

func newSAMLIDP(t *testing.T) *samlIDP {
	t.Helper()
	key, cert := selfSigned(t, "idp.example")
	return &samlIDP{
		t: t, key: key, cert: cert, entityID: "https://idp.example/metadata", ssoURL: "https://idp.example/sso",
		nameID: "una@example.com", groups: []string{"staff"}, notBefore: -time.Minute, notAfter: 5 * time.Minute,
		status: saml.StatusSuccess, signAssertion: true,
	}
}

func selfSigned(t *testing.T, cn string) (*rsa.PrivateKey, *x509.Certificate) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return key, cert
}

// metadata is what the operator is given for the provider: where to send people,
// and the certificate its assertions are signed with.
func (p *samlIDP) metadata() *saml.EntityDescriptor {
	certB64 := base64.StdEncoding.EncodeToString(p.cert.Raw)
	keyDesc := func(use string) saml.KeyDescriptor {
		return saml.KeyDescriptor{Use: use, KeyInfo: saml.KeyInfo{X509Data: saml.X509Data{X509Certificates: []saml.X509Certificate{{Data: certB64}}}}}
	}
	return &saml.EntityDescriptor{
		EntityID: p.entityID,
		IDPSSODescriptors: []saml.IDPSSODescriptor{{
			SSODescriptor: saml.SSODescriptor{RoleDescriptor: saml.RoleDescriptor{
				ProtocolSupportEnumeration: "urn:oasis:names:tc:SAML:2.0:protocol", KeyDescriptors: []saml.KeyDescriptor{keyDesc("signing")},
			}},
			SingleSignOnServices: []saml.Endpoint{{Binding: saml.HTTPRedirectBinding, Location: p.ssoURL}},
		}},
	}
}

// requestID reads the ID out of the AuthnRequest the browser was sent to the
// provider with: it is deflated and base64'd in the query.
func requestID(t *testing.T, redirect string) string {
	t.Helper()
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(u.Query().Get("SAMLRequest"))
	if err != nil {
		t.Fatal(err)
	}
	xmlBytes, err := io.ReadAll(flate.NewReader(bytes.NewReader(raw)))
	if err != nil {
		t.Fatal(err)
	}
	doc := etree.NewDocument()
	if err := doc.ReadFromBytes(xmlBytes); err != nil {
		t.Fatal(err)
	}
	return doc.Root().SelectAttrValue("ID", "")
}

func ts(d time.Duration) string { return time.Now().UTC().Add(d).Format("2006-01-02T15:04:05Z") }

var idCounter int

func freshID() string { idCounter++; return fmt.Sprintf("_id%d%d", time.Now().UnixNano(), idCounter) }

// assertionXML is the unsigned assertion.
func (p *samlIDP) assertionXML(acs, sp, inResponseTo string) string {
	aud := p.audience
	if aud == "" {
		aud = sp
	}
	rec := p.recipient
	if rec == "" {
		rec = acs
	}
	iss := p.issuer
	if iss == "" {
		iss = p.entityID
	}
	id := p.assertID
	if id == "" && !p.emptyAssertID {
		id = freshID()
	}
	var groups strings.Builder
	for _, g := range p.groups {
		fmt.Fprintf(&groups, `<saml:AttributeValue>%s</saml:AttributeValue>`, g)
	}
	return fmt.Sprintf(`<saml:Assertion xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="%s" Version="2.0" IssueInstant="%s">`+
		`<saml:Issuer>%s</saml:Issuer>`+
		`<saml:Subject><saml:NameID Format="urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress">%s</saml:NameID>`+
		`<saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer"><saml:SubjectConfirmationData NotOnOrAfter="%s" Recipient="%s" InResponseTo="%s"/></saml:SubjectConfirmation></saml:Subject>`+
		`<saml:Conditions NotBefore="%s" NotOnOrAfter="%s"><saml:AudienceRestriction><saml:Audience>%s</saml:Audience></saml:AudienceRestriction></saml:Conditions>`+
		`<saml:AuthnStatement AuthnInstant="%s" SessionIndex="_s1"><saml:AuthnContext><saml:AuthnContextClassRef>urn:oasis:names:tc:SAML:2.0:ac:classes:PasswordProtectedTransport</saml:AuthnContextClassRef></saml:AuthnContext></saml:AuthnStatement>`+
		`<saml:AttributeStatement><saml:Attribute Name="groups">%s</saml:Attribute><saml:Attribute Name="displayName"><saml:AttributeValue>Una Person</saml:AttributeValue></saml:Attribute></saml:AttributeStatement>`+
		`</saml:Assertion>`,
		id, ts(0), iss, p.nameID, ts(p.notAfter), rec, inResponseTo, ts(p.notBefore), ts(p.notAfter), aud, ts(0), groups.String())
}

func (p *samlIDP) signer() *dsig.SigningContext {
	key := p.key
	cert := p.cert
	if p.signWith != nil {
		key = p.signWith
	}
	if p.signedBy != nil {
		cert = p.signedBy
	}
	ks := dsig.TLSCertKeyStore(tls.Certificate{Certificate: [][]byte{cert.Raw}, PrivateKey: key})
	ctx := dsig.NewDefaultSigningContext(ks)
	ctx.Canonicalizer = dsig.MakeC14N10ExclusiveCanonicalizerWithPrefixList("")
	if err := ctx.SetSignatureMethod(dsig.RSASHA256SignatureMethod); err != nil {
		p.t.Fatal(err)
	}
	return ctx
}

func (p *samlIDP) sign(el *etree.Element) *etree.Element {
	p.t.Helper()
	signed, err := p.signer().SignEnveloped(el)
	if err != nil {
		p.t.Fatal(err)
	}
	return signed
}

func parseEl(t *testing.T, s string) *etree.Element {
	t.Helper()
	d := etree.NewDocument()
	if err := d.ReadFromString(s); err != nil {
		t.Fatal(err)
	}
	return d.Root()
}

// response is a complete, base64-encoded SAMLResponse for the request.
func (p *samlIDP) response(acs, sp, inResponseTo string) string {
	assertion := parseEl(p.t, p.assertionXML(acs, sp, inResponseTo))
	if p.signAssertion {
		assertion = p.sign(assertion)
	}
	if p.tamperName != "" {
		for _, n := range assertion.FindElements(".//NameID") {
			n.SetText(p.tamperName)
		}
	}
	return p.wrap(acs, inResponseTo, assertion)
}

// wrap puts assertions into a Response, and signs the response if asked.
func (p *samlIDP) wrap(acs, inResponseTo string, assertions ...*etree.Element) string {
	dest := p.dest
	if dest == "" {
		dest = acs
	}
	iss := p.issuer
	if iss == "" {
		iss = p.entityID
	}
	resp := parseEl(p.t, fmt.Sprintf(`<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="%s" Version="2.0" IssueInstant="%s" Destination="%s" InResponseTo="%s">`+
		`<saml:Issuer>%s</saml:Issuer><samlp:Status><samlp:StatusCode Value="%s"/></samlp:Status></samlp:Response>`,
		freshID(), ts(0), dest, inResponseTo, iss, p.status))
	for _, a := range assertions {
		resp.AddChild(a)
	}
	if p.signResponse {
		resp = p.sign(resp)
	}
	return p.encode(resp)
}

func (p *samlIDP) encode(el *etree.Element) string {
	d := etree.NewDocument()
	d.SetRoot(el)
	b, err := d.WriteToBytes()
	if err != nil {
		p.t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(b)
}
