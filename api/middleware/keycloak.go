package middleware

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	httputil2 "at.ourproject/vfeeg-backend/api/middleware/httputil"
	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/sirupsen/logrus"
)

var (
	kcConfig    map[string]*keycloakConfig
	verifier    *oidc.IDTokenVerifier
	kcClientAPI *KeycloakClient
)

type keycloakConfig struct {
	ClientId  string `json:"resource"`
	Secret    string `json:"secret,omitempty"`
	Realm     string `json:"realm"`
	Host      string `json:"auth-server-url"`
	Internal  bool   `json:"issuer-internal,omitempty"`
	IssuerUrl string `json:"issuer-url,omitempty"`
}

func InitKeycloak() {
	logrus.Printf("InitKeycloak")
	var err error
	kcConfig, err = readKeycloakConfig()
	if err != nil {
		logrus.WithField("error", "Init Keycloak").Errorf("E: %v", err)
		panic(err)
	}

	if _, ok := kcConfig["api"]; !ok {
		logrus.WithField("error", "Init Keycloak").Errorf("E: %v", errors.New("no client-id 'at.ourproject.vfeeg.api' available"))
		panic(err)
	}

	clientIDApi := kcConfig["api"].ClientId
	clientSecretApi := kcConfig["api"].Secret

	realmApi := kcConfig["api"].Realm
	host := strings.TrimRight(kcConfig["api"].Host, "/")
	issuerUrl := kcConfig["api"].IssuerUrl

	c := &http.Client{Timeout: time.Duration(1) * time.Second}
	kcClientAPI, err = NewKeycloakClient(fmt.Sprintf("%s/realms/%s", host, realmApi), clientIDApi, clientSecretApi, issuerUrl, c)
	if err != nil {
		panic(err)
	}

	/**
	set up jwt token verifier
	*/
	clientIDApp := kcConfig["app"].ClientId
	realmApp := kcConfig["app"].Realm
	//issuerUrl = kcConfig["app"].IssuerUrl

	hostApp := strings.TrimRight(kcConfig["app"].Host, "/")

	logrus.Printf("Keycloak configuration:")
	logrus.Printf("         Host: %s", hostApp)
	logrus.Printf("     Internal: %v", kcConfig["app"].Internal)
	logrus.Printf("   Issuer-Url: %v", kcConfig["app"].IssuerUrl)
	logrus.Printf("    Client-ID: %v", clientIDApp)
	logrus.Printf("        Realm: %v", realmApp)

	ctx := context.Background()
	if kcConfig["app"].Internal {
		internalHost := kcConfig["app"].IssuerUrl // Custom transport that rewrites DNS lookups
		if internalHost == "" {
			panic("issuerUrl is required")
		}
		// External issuer (MUST match the token's "iss")
		u, err := url.Parse(hostApp)
		if err != nil {
			panic(err)
		}
		logrus.WithContext(ctx).Printf("Internal KEYCLOAK host: %s\n", u.Host)
		transport := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				// addr looks like "auth.example.com:443"
				if strings.HasPrefix(addr, u.Host) {
					// Replace with internal Docker hostname
					addr = internalHost
				}
				d := net.Dialer{Timeout: 5 * time.Second}
				return d.DialContext(ctx, network, addr)
			},
		}

		// Custom HTTP client using the resolver
		httpClient := &http.Client{Transport: transport, Timeout: 10 * time.Second}

		// Inject client into OIDC context
		ctx = oidc.ClientContext(ctx, httpClient)
	}
	providerUriApp := fmt.Sprintf("%s/realms/%s", hostApp, realmApp)
	provider, err := oidc.NewProvider(ctx, providerUriApp)
	if err != nil {
		logrus.WithContext(ctx).Errorf("E: %v", err)
		panic(err)
	}
	verifier = provider.Verifier(&oidc.Config{ClientID: clientIDApp, SkipClientIDCheck: true, SupportedSigningAlgs: []string{oidc.RS256}})
}

func readKeycloakConfig() (map[string]*keycloakConfig, error) {
	kcPath, ok := os.LookupEnv("KEYCLOAK_CONFIG")
	if !ok {
		kcPath = "./keycloak.json"
	}
	kcConfigFile, err := os.Open(kcPath)
	if err != nil {
		return nil, err
	}
	defer kcConfigFile.Close()

	payload, err := io.ReadAll(kcConfigFile)
	if err != nil {
		return nil, err
	}

	kcConfig := map[string]*keycloakConfig{}
	err = json.Unmarshal(payload, &kcConfig)
	return kcConfig, err
}

type KeycloakClient struct {
	oidc         *oidc.Provider
	verifier     *oidc.IDTokenVerifier
	clientID     string
	clientSecret string
	client       *http.Client
}

func NewKeycloakClient(issuer, clientID, clientSecret, issuerUrl string, client *http.Client) (*KeycloakClient, error) {
	kc := &KeycloakClient{
		clientID:     clientID,
		clientSecret: clientSecret,
		client:       client,
	}

	ctx := oidc.ClientContext(context.Background(), client)
	if issuerUrl != "" {
		ctx = oidc.InsecureIssuerURLContext(ctx, issuerUrl)
	}

	var err error
	kc.oidc, err = oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	kc.verifier = kc.oidc.Verifier(&oidc.Config{ClientID: clientID, SkipClientIDCheck: true, SupportedSigningAlgs: []string{oidc.RS256}})
	return kc, nil
}

type Credentials struct {
	IDToken      string `json:"id_token,omitempty"`
	AccessToken  string `json:"access_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	IssueUrl     string `json:"issue_url,omitempty"`
}

func (kc *KeycloakClient) AuthenticateUserWithPassword(username, password string) (token *oidc.IDToken, err error) {
	params := map[string][]string{
		"grant_type":    {"password"},
		"client_id":     {kc.clientID},
		"client_secret": {kc.clientSecret},
		"scope":         {"openid"},
		"username":      {username},
		"password":      {password},
	}
	resp, err := httputil2.PostFormUrlencoded(kc.client, kc.oidc.Endpoint().TokenURL, nil, params)
	if err != nil {
		return
	}
	tok := &Credentials{}
	if err = httputil2.DecodeJSONResponse(resp, tok); err != nil {
		return
	}

	token, err = kc.VerifyToken(tok.IDToken)
	return
}

func (kc *KeycloakClient) VerifyToken(token string) (*oidc.IDToken, error) {
	return kc.verifier.Verify(context.Background(), token)
}
