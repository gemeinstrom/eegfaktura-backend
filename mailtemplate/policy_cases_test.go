package mailtemplate

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// policyCases is mail-html-policy-cases.json, shared byte-identically with eegfaktura-billing.
type policyCases struct {
	Version int `json:"version"`
	Context struct {
		OwnImageId     string `json:"ownImageId"`
		ForeignImageId string `json:"foreignImageId"`
	} `json:"context"`
	Cases []struct {
		Name        string   `json:"name"`
		Input       string   `json:"input"`
		Reject      bool     `json:"reject"`
		Contains    []string `json:"contains"`
		NotContains []string `json:"notContains"`
	} `json:"cases"`
}

func TestSharedPolicyCases(t *testing.T) {
	data, err := os.ReadFile("mail-html-policy-cases.json")
	require.NoError(t, err)
	var pc policyCases
	require.NoError(t, json.Unmarshal(data, &pc))
	require.Equal(t, 1, pc.Version)
	require.GreaterOrEqual(t, len(pc.Cases), 46)

	def := Lookup(KeyConsentRequest) // has eeg.name, eeg.email and the block eeg.contactBlock, like billing-document
	own := func(id string) bool { return id == pc.Context.OwnImageId }
	names := map[string]bool{}
	for _, c := range pc.Cases {
		require.False(t, names[c.Name], "duplicate case %q", c.Name)
		names[c.Name] = true
		t.Run(c.Name, func(t *testing.T) {
			out, err := SanitizeBody(def, c.Input, own)
			if c.Reject {
				var verr *ValidationError
				assert.True(t, errors.As(err, &verr), "expected a rejection, got %q", out)
				return
			}
			require.NoError(t, err)
			for _, s := range c.Contains {
				assert.Contains(t, out, s)
			}
			for _, s := range c.NotContains {
				assert.NotContains(t, out, s)
			}
		})
	}
}
