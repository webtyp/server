package httpd

import (
	"strings"

	"webtyp.com/json"
	"webtyp.com/model"
	"webtyp.com/router"
)

const contentTypeFormUrlEncoded = "application/x-www-form-urlencoded"

func decodeBody(c *httpContext, into model.Decodable) error {
	ct := c.GetHeader("Content-Type")
	if idx := strings.IndexByte(ct, ';'); idx != -1 {
		ct = ct[:idx]
	}
	ct = strings.TrimSpace(ct)

	if strings.EqualFold(ct, contentTypeFormUrlEncoded) {
		into.DecodeFields(router.QueryReader(string(c.Body())))
		return nil
	}

	return json.Decode(c.Body(), into)
}
