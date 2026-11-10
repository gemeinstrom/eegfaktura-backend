package middleware

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tracking 1: the keycloak.json in the repository is a sample. The real file, with the
// secret of the api client, is mounted at run time (KEYCLOAK_CONFIG).
func TestRepositoryKeycloakJsonHoldsNoSecret(t *testing.T) {
	t.Setenv("KEYCLOAK_CONFIG", "../../keycloak.json")
	cfg, err := readKeycloakConfig()
	require.NoError(t, err)
	require.Contains(t, cfg, "api")
	// assert.True, not Equal: a failure must not print the value
	assert.True(t, cfg["api"].Secret == "<secret of the at.ourproject.vfeeg.api client>",
		"keycloak.json must carry the placeholder, not a client secret")
}
