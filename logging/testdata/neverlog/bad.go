// Package neverlog is the fixture of TestNeverLog_FindsTheFixture. It is
// compiled by the test only: go build ignores testdata. Lines marked with
// "// want" are hits, all others must stay quiet.
package neverlog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

type member struct {
	FirstName string
	IBAN      string
	Number    string
}

type nested struct{ Members []*member }

func run(ctx context.Context, m *member, n nested, err error, id string, count int, when time.Time, format string, list []member, a any, h http.Header, username, sub, subscription, apiKey, userID, tokenString, tokenizerState, accesstoken, userpassword string, tokens, IBANs []string, tokenErr error, payload []byte, body *bytes.Buffer, emailVerified bool) {
	// structs printed whole
	logrus.Infof("member %v", m)                      // want
	logrus.Warnf("member %+v", *m)                    // want
	logrus.WithContext(ctx).Errorf("nested %#v", n)   // want
	logrus.Info(m)                                    // want
	logrus.Debugf("list %s", list)                    // want
	logrus.WithField("MSG", m).Info("x")              // want
	logrus.WithFields(logrus.Fields{"MSG": n}).Warn() // want
	logrus.Infof(format, m)                           // want
	fmt.Printf("%v\n", m)                             // want
	fmt.Println(n)                                    // want

	logrus.Infof("value %+v", a)         // want
	logrus.Infof("headers %v", h)        // want
	logrus.WithField("MSG", h).Info("x") // want
	logrus.Infof("value %v", a)
	logrus.Errorf("failed: %+v", err)

	// names: identifiers split into words, compared as whole words
	logrus.Warnf("x %s", m.FirstName)                     // want
	logrus.WithContext(ctx).Warn(m.IBAN)                  // want
	logrus.Errorf("x %s", fmt.Sprintf("%s", m.FirstName)) // want
	logrus.Warnf("x %s", username)                        // want
	logrus.Warnf("x %s", sub)                             // want
	slog.Info("x", "key", apiKey)                         // want
	logrus.WithField("MSG", userID).Info("x")             // want
	logrus.Infof("x %s", tokenString)                     // want
	logrus.Info(tokenErr)                                 // an error value, skipped by type
	logrus.Info(body.String())                            // want
	logrus.Infof("verified %t", emailVerified)            // a boolean
	logrus.Infof("tokens %d", len(tokens))
	logrus.Infof("subscription %s", subscription)
	logrus.Infof("x %s", tokenizerState)
	logrus.Infof("x %v", tokens)                            // want
	logrus.Infof("x %v", IBANs)                             // want
	logrus.Infof("x %s", accesstoken)                       // want
	logrus.Infof("x %s", userpassword)                      // want
	logrus.Infof("x %s %d", tokenErr.Error(), len(payload)) // the text of an error value
	logrus.WithField("tenant", id).Warnf("token %%v %d", 1) // the verb is escaped, %d is count-like

	// a list word right before a verb of a format text
	logrus.Infof("email: %s", id)        // want
	logrus.Infof("password=%s", id)      // want
	fmt.Printf("IBANs %v\n", id)         // want
	logrus.Infof("mail address: %s", id) // want
	logrus.Infof("subscription %s, the token expired", id)

	// otherwise string literals are not scanned: a key outside allowedFields is dropped at runtime
	logrus.WithField("iban", id).Info("x")
	slog.Info("x", slog.String("email", id))
	slog.WarnContext(ctx, "x", "password", id)
	logrus.WithField("user_id", id).Info("x")

	// fine
	logrus.Warn("the token expired, no password set")
	logrus.Infof("id %s count %d since %v", id, count, when)
	logrus.Infof("type %T of %p", m, m)
	logrus.WithError(err).Error("failed")
	logrus.Errorf("failed: %v", err)
	logrus.Errorf("failed: %v", errors.New("boom"))
	logrus.WithField("tenant", id).Info(m.Number)
	slog.Info("email sent", slog.String("request.id", id))
}
