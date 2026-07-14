package runtime

import (
	"bytes"
	"io"
	"net/http"
)

// readAndRestore reads a request body and puts it back, so the request can still
// be sent onward.
func readAndRestore(req *http.Request) (string, error) {
	if req.Body == nil {
		return "", nil
	}
	b, err := io.ReadAll(req.Body)
	if err != nil {
		return "", err
	}
	req.Body = io.NopCloser(bytes.NewReader(b))
	return string(b), nil
}
