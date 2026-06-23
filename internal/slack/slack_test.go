package slack

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

// AGENTDONE_STDOUT=1 prints the message and short-circuits before any POST (the
// URL here would fail/hang if it were actually contacted).
func TestPostStdout(t *testing.T) {
	t.Setenv("AGENTDONE_STDOUT", "1")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	postErr := Post("http://127.0.0.1:0/never-contacted", "hello-stdout")
	_ = w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)
	if postErr != nil {
		t.Fatalf("Post in stdout mode = %v, want nil", postErr)
	}
	if !strings.Contains(string(out), "hello-stdout") {
		t.Errorf("stdout = %q, want it to contain the message", string(out))
	}
}

// A transport failure must return an error with the webhook secret redacted —
// the URL IS the secret, and callers (e.g. `init`'s "Webhook test failed: %v")
// print the returned error to stdout / CI logs.
func TestPostRedactsSecretInReturnedError(t *testing.T) {
	t.Setenv("AGENTDONE_STDOUT", "")
	const secret = "SuperSecretTokenXYZ"
	// Port 1 is unbound: client.Post fails with a *url.Error embedding the URL.
	err := Post("https://127.0.0.1:1/services/T0/B1/"+secret, "hi")
	if err == nil {
		t.Fatal("Post to an unreachable webhook returned nil, want an error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("returned error leaks the webhook secret: %v", err)
	}
}

// transcript-derived text must not reach Slack with live control
// characters — "<!channel>" would mention everyone, "<url|label>" would render
// a disguised link, and "Vec<T>" would silently disappear from the message.
func TestPostEscapesSlackControlCharacters(t *testing.T) {
	var got struct {
		Text string `json:"text"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &got); err != nil {
			t.Errorf("payload is not JSON: %v", err)
		}
	}))
	defer srv.Close()
	if err := Post(srv.URL, `done <!channel> & fixed Vec<T>`); err != nil {
		t.Fatalf("Post: %v", err)
	}
	if want := `done &lt;!channel&gt; &amp; fixed Vec&lt;T&gt;`; got.Text != want {
		t.Errorf("text = %q, want %q", got.Text, want)
	}
}

// a transport error's message embeds the full webhook URL — which IS
// the secret. The debug log line must carry only scheme://host.
func TestRedactStripsWebhookPath(t *testing.T) {
	secret := "https://hooks.slack.com/services/T000/B000/SECRETTOKEN"
	in := &url.Error{Op: "Post", URL: secret, Err: errors.New("dial tcp: connection refused")}
	got := redact(in).Error()
	if strings.Contains(got, "SECRETTOKEN") || strings.Contains(got, "/services/") {
		t.Errorf("redact leaked the webhook path: %s", got)
	}
	for _, want := range []string{"hooks.slack.com", "connection refused"} {
		if !strings.Contains(got, want) {
			t.Errorf("redact dropped %q from: %s", want, got)
		}
	}
	plain := errors.New("plain failure")
	if redact(plain) != plain {
		t.Error("redact must pass a non-url.Error through unchanged")
	}
}

func TestPostStatusHandling(t *testing.T) {
	t.Run("2xx is success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()
		if err := Post(srv.URL, "hi"); err != nil {
			t.Fatalf("Post on 200 = %v, want nil", err)
		}
	})

	t.Run("4xx/5xx is surfaced as an error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "no_service", http.StatusNotFound)
		}))
		defer srv.Close()
		if err := Post(srv.URL, "hi"); err == nil {
			t.Fatal("Post on 404 = nil, want an error (invalid webhook must not look successful)")
		}
	})

	t.Run("empty URL is a no-op", func(t *testing.T) {
		if err := Post("", "hi"); err != nil {
			t.Fatalf("Post with empty URL = %v, want nil", err)
		}
	})
}
