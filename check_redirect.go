// +build !go1.8

package eventsource

import (
	"errors"
	"net/http"
)

func setCheckRedirect(c *http.Client) {
	c.CheckRedirect = checkRedirect
}

// Go's http package doesn't copy headers across when it encounters
// redirects so we need to do that manually.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	for k, vv := range via[0].Header {
		for _, v := range vv {
			req.Header.Add(k, v)
		}
	}
	return nil
}
