package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/crewjam/saml"
	"gopkg.in/yaml.v3"

	"github.com/nitpick-oss/nitpick/internal/server"
	"github.com/nitpick-oss/nitpick/internal/server/auth"
)

// fileConfig is the server's configuration file. It holds how people sign in and
// what each of them may do; the notary's own flags stay flags, as they were on
// the notary command, so a deployment changes its binary name and nothing else.
//
// Keys it does not know are an error. A typo in a security setting that was
// quietly ignored is the setting off.
type fileConfig struct {
	// Components lists what to run. Empty is the notary.
	Components []string `yaml:"components"`

	Session struct {
		Idle     time.Duration `yaml:"idle"`
		Absolute time.Duration `yaml:"absolute"`
	} `yaml:"session"`

	Auth struct {
		// TrustForwardedFor reads a client's address from X-Forwarded-For, for the
		// password limiter. Only behind a proxy that overwrites the header.
		TrustForwardedFor bool `yaml:"trust_forwarded_for"`

		Password *struct {
			UsersFile string `yaml:"users_file"`
		} `yaml:"password"`

		OIDC *struct {
			Issuer           string   `yaml:"issuer"`
			ClientID         string   `yaml:"client_id"`
			ClientSecretFile string   `yaml:"client_secret_file"`
			RedirectURL      string   `yaml:"redirect_url"`
			Scopes           []string `yaml:"scopes"`
			GroupsClaim      string   `yaml:"groups_claim"`
			Label            string   `yaml:"label"`
		} `yaml:"oidc"`

		SAML *struct {
			IDPMetadataFile string `yaml:"idp_metadata_file"`
			EntityID        string `yaml:"entity_id"`
			ACSURL          string `yaml:"acs_url"`
			KeyFile         string `yaml:"key_file"`
			CertFile        string `yaml:"cert_file"`
			GroupsAttribute string `yaml:"groups_attribute"`
			Label           string `yaml:"label"`
		} `yaml:"saml"`
	} `yaml:"auth"`

	Roles []struct {
		Role     string `yaml:"role"`
		Provider string `yaml:"provider"`
		User     string `yaml:"user"`
		Group    string `yaml:"group"`
	} `yaml:"roles"`
}

// loadConfig reads and checks a configuration file. An empty path is no file: no
// sign-in, so no UI, which is how the notary alone has always run.
func loadConfig(path string) (*fileConfig, error) {
	cfg := &fileConfig{}
	if path == "" {
		return cfg, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read the configuration: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(cfg); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := cfg.check(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// knownComponents are the components this binary can run.
var knownComponents = map[string]bool{"notary": true}

func (c *fileConfig) check() error {
	seen := map[string]bool{}
	for _, name := range c.Components {
		if !knownComponents[name] {
			return fmt.Errorf("components: %q is not a component of this server", name)
		}
		if seen[name] {
			return fmt.Errorf("components: %q is listed twice", name)
		}
		seen[name] = true
	}
	if c.Session.Idle < 0 || c.Session.Absolute < 0 {
		return errors.New("session: timeouts must not be negative")
	}
	for i, r := range c.Roles {
		if r.Provider != "" && !c.hasProvider(r.Provider) {
			return fmt.Errorf("roles[%d]: provider %q is not configured under auth", i, r.Provider)
		}
	}
	return nil
}

func (c *fileConfig) hasProvider(id string) bool {
	switch id {
	case "password":
		return c.Auth.Password != nil
	case "oidc":
		return c.Auth.OIDC != nil
	case "saml":
		return c.Auth.SAML != nil
	}
	return false
}

// components is the list to run, with the default.
func (c *fileConfig) components() []string {
	if len(c.Components) == 0 {
		return []string{"notary"}
	}
	return c.Components
}

// roleRules turns the file's role section into the server's rules.
func (c *fileConfig) roleRules() []server.RoleRule {
	var rules []server.RoleRule
	for _, r := range c.Roles {
		rules = append(rules, server.RoleRule{
			Roles: []server.Role{server.Role(r.Role)}, Provider: r.Provider, User: r.User, Group: r.Group,
		})
	}
	return rules
}

// providers builds every configured sign-in provider. Each is built here, at
// start-up, so one that cannot work stops the process and is not found by the
// first person to try it.
func (c *fileConfig) providers(ctx context.Context, log *slog.Logger) ([]server.Provider, error) {
	var out []server.Provider

	if p := c.Auth.Password; p != nil {
		if p.UsersFile == "" {
			return nil, errors.New("auth.password: users_file is required")
		}
		f, err := os.Open(p.UsersFile)
		if err != nil {
			return nil, fmt.Errorf("auth.password: %w", err)
		}
		defer func() { _ = f.Close() }()
		if info, err := f.Stat(); err == nil && info.Mode().Perm()&0o007 != 0 {
			log.Warn("the users file is readable by every account on this machine", "file", p.UsersFile, "mode", info.Mode().Perm().String())
		}
		users, err := auth.ParseUsers(f)
		if err != nil {
			return nil, fmt.Errorf("auth.password: %w", err)
		}
		pw := auth.NewPassword(users, log)
		pw.TrustForwardedFor = c.Auth.TrustForwardedFor
		out = append(out, pw)
	}

	if o := c.Auth.OIDC; o != nil {
		secret := ""
		if o.ClientSecretFile != "" {
			b, err := os.ReadFile(o.ClientSecretFile)
			if err != nil {
				return nil, fmt.Errorf("auth.oidc: %w", err)
			}
			secret = strings.TrimSpace(string(b))
		}
		p, err := auth.NewOIDC(ctx, auth.OIDCConfig{
			Issuer: o.Issuer, ClientID: o.ClientID, ClientSecret: secret, RedirectURL: o.RedirectURL,
			Scopes: o.Scopes, GroupsClaim: o.GroupsClaim, Label: o.Label,
			TrustForwardedFor: c.Auth.TrustForwardedFor,
		}, log)
		if err != nil {
			return nil, fmt.Errorf("auth.oidc: %w", err)
		}
		out = append(out, p)
	}

	if s := c.Auth.SAML; s != nil {
		p, err := buildSAML(s.IDPMetadataFile, s.EntityID, s.ACSURL, s.KeyFile, s.CertFile, s.GroupsAttribute, s.Label, c.Auth.TrustForwardedFor, log)
		if err != nil {
			return nil, fmt.Errorf("auth.saml: %w", err)
		}
		out = append(out, p)
	}
	return out, nil
}

func buildSAML(metadataFile, entityID, acs, keyFile, certFile, groups, label string, trustForwardedFor bool, log *slog.Logger) (*auth.SAML, error) {
	if metadataFile == "" || entityID == "" || acs == "" || keyFile == "" || certFile == "" {
		return nil, errors.New("idp_metadata_file, entity_id, acs_url, key_file and cert_file are all required")
	}
	raw, err := os.ReadFile(metadataFile)
	if err != nil {
		return nil, err
	}
	meta, err := parseIDPMetadata(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", metadataFile, err)
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return nil, err
	}
	signer, ok := pair.PrivateKey.(crypto.Signer)
	if !ok {
		return nil, errors.New("the key cannot sign")
	}
	acsURL, err := url.Parse(acs)
	if err != nil {
		return nil, fmt.Errorf("acs_url: %w", err)
	}
	return auth.NewSAML(auth.SAMLConfig{
		IDPMetadata: meta, EntityID: entityID, ACSURL: acsURL, Key: signer, Certificate: cert,
		GroupsAttribute: groups, Label: label, TrustForwardedFor: trustForwardedFor,
	}, log)
}

// parseIDPMetadata reads a provider's metadata, whether it is one entity or a
// federation's list of them, and takes the first that describes an identity
// provider.
func parseIDPMetadata(raw []byte) (*saml.EntityDescriptor, error) {
	var one saml.EntityDescriptor
	if err := xml.Unmarshal(raw, &one); err == nil && len(one.IDPSSODescriptors) > 0 {
		return &one, nil
	}
	var many saml.EntitiesDescriptor
	if err := xml.Unmarshal(raw, &many); err == nil {
		for i := range many.EntityDescriptors {
			if len(many.EntityDescriptors[i].IDPSSODescriptors) > 0 {
				return &many.EntityDescriptors[i], nil
			}
		}
	}
	return nil, errors.New("no identity provider described")
}
