package registries

import (
	"io"
	"net/http"
)

// rawGet performs a generic HTTP GET. headers map is applied as-is.
func rawGet(rawURL string, headers map[string]string) ([]byte, int, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return nil, 0, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	return body, resp.StatusCode, nil
}
