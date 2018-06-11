// +build go1.8

package eventsource

import "net/http"

// On go1.8 we don't need to do anything because it will copy headers on redirects
func setCheckRedirect(c *http.Client) {
}
