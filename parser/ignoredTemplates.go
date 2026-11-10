package parser

import (
	"os"
	"path/filepath"
	"sort"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// memberMailConfigs are the template configs the member mails used to read from the data volume.
var memberMailConfigs = []string{"activation-mail-template.toml", "zp-complete-mail-template.toml", InactiveMailTemplate}

// IgnoredVolumeTemplates lists the hand-made member-mail templates still lying on the data volume
// (`<base>/<TENANT>/templates/<config>` and the global `<base>/templates/<config>`). Since 1.9.0 the send
// path ignores them; a file found here means its community sends the default text until it saves its own text.
func IgnoredVolumeTemplates(base string) []string {
	var found []string
	dirs, _ := filepath.Glob(filepath.Join(base, "*", "templates"))
	dirs = append(dirs, filepath.Join(base, "templates"))
	for _, dir := range dirs {
		for _, cfg := range memberMailConfigs {
			p := filepath.Join(dir, cfg)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				rel, _ := filepath.Rel(base, p)
				found = append(found, filepath.ToSlash(rel))
			}
		}
	}
	sort.Strings(found)
	return found
}

// WarnIgnoredVolumeTemplates logs once at startup which volume templates are no longer used, so a
// community that still relies on one shows up in the log instead of silently changing member mails.
func WarnIgnoredVolumeTemplates() {
	base := viper.GetString("file-content.templates")
	if base == "" {
		return
	}
	if found := IgnoredVolumeTemplates(base); len(found) > 0 {
		log.WithField("templates", found).Warnf("%d hand-made mail template file(s) on the volume are ignored "+
			"since 1.9.0; communities without a saved text send the default",
			len(found))
	}
}
