// Command mailtemplate-import moves the hand-made member mail templates of the
// data volume (<base>/<TENANT>/templates) into the editable mail texts of the
// backend (konzept-mail-texte-editierbar.md, G.4). See README.md.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// tokenEnv holds the bearer token (superuser with EEG_ADMIN group); never a flag, so it stays out of the shell history.
const tokenEnv = "MAILTPL_IMPORT_TOKEN"

func main() {
	base := flag.String("base", "", "directory with <TENANT>/templates/* (a copy of the data volume)")
	api := flag.String("api", "", "backend base URL, e.g. https://host/api (without /eeg)")
	tenants := flag.String("tenants", "", "comma-separated tenants (default: every directory under -base)")
	dryRun := flag.Bool("dry-run", false, "convert and print, write nothing")
	overwrite := flag.Bool("overwrite", false, "replace a mail the community has already customised")
	testMail := flag.String("test-mail", "", `after the import send a test mail: "eeg" (community address) or "me"`)
	flag.Parse()

	if *base == "" || (!*dryRun && *api == "") {
		flag.Usage()
		os.Exit(2)
	}
	if *testMail != "" && *testMail != "eeg" && *testMail != "me" {
		fmt.Fprintln(os.Stderr, `-test-mail must be "eeg" or "me"`)
		os.Exit(2)
	}
	var client *apiClient
	if !*dryRun {
		token := strings.TrimSpace(os.Getenv(tokenEnv))
		if token == "" {
			fmt.Fprintf(os.Stderr, "%s is not set\n", tokenEnv)
			os.Exit(2)
		}
		client = &apiClient{base: strings.TrimRight(*api, "/"), token: token, http: &http.Client{Timeout: 60 * time.Second}}
	}

	list, err := tenantList(*base, *tenants)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	failed := 0
	for _, tenant := range list {
		dir := filepath.Join(*base, tenant, "templates")
		convs := ConvertTenant(os.DirFS(dir), tenant)
		if len(convs) == 0 {
			fmt.Printf("== %s: keine Mail-Vorlage in %s\n", tenant, dir)
			continue
		}
		for _, c := range convs {
			printConversion(c, *dryRun)
			if c.Blocked() {
				failed++
				fmt.Printf("   -> NICHT übernommen\n\n")
				continue
			}
			if *dryRun {
				fmt.Println()
				continue
			}
			if err := client.importMail(c, *overwrite, *testMail); err != nil {
				failed++
				fmt.Printf("   -> FEHLER: %v\n\n", err)
				continue
			}
			fmt.Printf("   -> übernommen\n\n")
		}
	}
	if failed > 0 {
		fmt.Printf("%d Mail(s) nicht übernommen\n", failed)
		os.Exit(1)
	}
}

func tenantList(base, flagValue string) ([]string, error) {
	if flagValue != "" {
		var out []string
		for _, t := range strings.Split(flagValue, ",") {
			if t = strings.TrimSpace(t); t != "" {
				out = append(out, strings.ToUpper(t))
			}
		}
		return out, nil
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && e.Name() != "templates" {
			if _, err := os.Stat(filepath.Join(base, e.Name(), "templates")); err == nil {
				out = append(out, e.Name())
			}
		}
	}
	sort.Strings(out)
	return out, nil
}

func printConversion(c *Conversion, withBody bool) {
	fmt.Printf("== %s / %s (%s)\n", c.Tenant, c.Key, c.Config)
	for _, f := range c.Findings {
		mark := "Hinweis"
		if f.Blocking {
			mark = "STOP   "
		}
		fmt.Printf("   %s %s\n", mark, f.Text)
	}
	for _, f := range c.Files {
		fmt.Printf("   Datei   %s %s (%s, %d KB)\n", f.Kind, f.Filename, f.MimeType, len(f.Content)/1024)
	}
	if withBody && !c.Blocked() {
		fmt.Printf("   Betreff %s\n--- bodyHtml ---\n%s\n--- Ende ---\n", c.Subject, c.Body)
	}
}

type apiClient struct {
	base  string
	token string
	http  *http.Client
}

func (a *apiClient) do(tenant, method, path string, body io.Reader, contentType string, out interface{}) (int, error) {
	req, err := http.NewRequest(method, a.base+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("tenant", tenant)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("%s %s: %d %s", method, path, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, err
		}
	}
	return resp.StatusCode, nil
}

func (a *apiClient) json(tenant, method, path string, in, out interface{}) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	_, err := a.do(tenant, method, path, body, "application/json", out)
	return err
}

// importMail uploads the files as drafts, saves the text and optionally sends
// a test mail. The saved version of the community is only replaced by the PUT
// itself (with its current version): if anything fails before, it stays
// untouched and the uploaded drafts are deleted again.
func (a *apiClient) importMail(c *Conversion, overwrite bool, testMail string) error {
	path := "/eeg/mailtemplates/" + c.Key
	var current struct {
		Customized bool `json:"customized"`
		Version    int  `json:"version"`
	}
	if err := a.json(c.Tenant, "GET", path, nil, &current); err != nil {
		return err
	}
	if current.Customized && !overwrite {
		return errors.New("die Gemeinschaft hat diese Mail schon angepasst (-overwrite ersetzt sie)")
	}

	var drafts []string
	cleanup := func() {
		for _, id := range drafts {
			_ = a.json(c.Tenant, "DELETE", path+"/files/"+id, nil, nil)
		}
	}
	body := c.Body
	var attachmentId interface{}
	for _, f := range c.Files {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("kind", f.Kind)
		fw, err := mw.CreateFormFile("file", f.Filename)
		if err != nil {
			cleanup()
			return err
		}
		_, _ = fw.Write(f.Content)
		_ = mw.Close()
		var uploaded struct {
			Id string `json:"id"`
		}
		if _, err = a.do(c.Tenant, "POST", path+"/files", &buf, mw.FormDataContentType(), &uploaded); err != nil {
			cleanup()
			return err
		}
		drafts = append(drafts, uploaded.Id)
		if f.Kind == "attachment" {
			attachmentId = uploaded.Id
		}
		body = strings.ReplaceAll(body, "mailfile:"+f.PreId, "mailfile:"+uploaded.Id)
	}

	version := 0
	if current.Customized {
		version = current.Version
	}
	if err := a.json(c.Tenant, "PUT", path, map[string]interface{}{
		"subject": c.Subject, "bodyHtml": body, "version": version, "attachmentFileId": attachmentId}, nil); err != nil {
		cleanup()
		return err
	}
	if testMail != "" {
		var sent struct {
			SentTo string `json:"sentTo"`
		}
		if terr := a.json(c.Tenant, "POST", path+"/test", map[string]interface{}{
			"recipient": testMail, "subject": c.Subject, "bodyHtml": body}, &sent); terr != nil {
			fmt.Printf("   Testmail fehlgeschlagen (Import bleibt): %v\n", terr)
		} else {
			fmt.Printf("   Testmail an %s\n", sent.SentTo)
		}
	}
	return nil
}
