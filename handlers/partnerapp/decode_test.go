package partnerapp

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/router"
)

func TestDecodeBodyEndingWithDataAndEOF(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"valid", `{"value":"accepted"}`, true},
		{"trailing whitespace", `{"value":"accepted"} `, true},
		{"second document", `{"value":"accepted"} {}`, false},
		{"unknown field", `{"value":"accepted","unknown":true}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/", nil)
			request.Body = &router.RereadableReadCloser{ReadCloser: io.NopCloser(iotest.DataErrReader(strings.NewReader(tc.body)))}
			defer request.Body.Close()
			event := &core.RequestEvent{Event: router.Event{Request: request}}
			var input struct {
				Value string `json:"value"`
			}
			err := decode(event, &input)
			if (err == nil) != tc.valid {
				t.Fatalf("decode returned %v, valid=%v", err, tc.valid)
			}
			if tc.valid && input.Value != "accepted" {
				t.Fatalf("unexpected value %q", input.Value)
			}
		})
	}
}
