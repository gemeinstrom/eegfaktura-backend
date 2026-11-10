package config

import (
	"os"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadActivationMailTemplateConfig(t *testing.T) {
	config, err := ReadActivationMailTemplateConfig(os.DirFS("../public/templates"), "activation-mail-template.toml")
	assert.NoError(t, err)
	assert.Equal(t, "AktivierungsEmail-template.html", config.TemplateFile)
	assert.Empty(t, config.InlinePictures, "no logo since default version 2")

	// volume configs read by cmd/mailtemplate-import still name their pictures
	fsys := fstest.MapFS{"a.toml": {Data: []byte(`TemplateFile="a.html"
InlinePictures = [
  { Filepath="eegfaktura-logo.png", contentId="eegfaktura-logo-1" }
]`)}}
	config, err = ReadActivationMailTemplateConfig(fsys, "a.toml")
	assert.NoError(t, err)
	require.Equal(t, 1, len(config.InlinePictures))
	assert.Equal(t, "eegfaktura-logo.png", config.InlinePictures[0].Filepath)
}
