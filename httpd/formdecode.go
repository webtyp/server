package httpd

import (
	"net/url"
	"strconv"
	"strings"

	"webtyp.com/json"
	"webtyp.com/model"
)

const contentTypeFormUrlEncoded = "application/x-www-form-urlencoded"

type formReader struct {
	values url.Values
}

var _ model.FieldReader = (*formReader)(nil)

func (fr *formReader) String(name string) (string, bool) {
	vals, ok := fr.values[name]
	if !ok || len(vals) == 0 {
		return "", false
	}
	return vals[0], true
}

func (fr *formReader) Raw(name string) (string, bool) {
	return fr.String(name)
}

func (fr *formReader) Int(name string) (int64, bool) {
	s, ok := fr.String(name)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func (fr *formReader) Float(name string) (float64, bool) {
	s, ok := fr.String(name)
	if !ok {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

func (fr *formReader) Bool(name string) (bool, bool) {
	s, ok := fr.String(name)
	if !ok {
		return false, false
	}
	switch strings.ToLower(s) {
	case "true", "on", "1":
		return true, true
	case "false", "off", "0", "":
		return false, true
	default:
		return false, false
	}
}

func (fr *formReader) Bytes(name string) ([]byte, bool) {
	s, ok := fr.String(name)
	if !ok {
		return nil, false
	}
	return []byte(s), true
}

func (fr *formReader) Object(name string, into model.Decodable) bool {
	return false
}

func (fr *formReader) Array(name string) (model.ArrayReader, bool) {
	return nil, false
}

func decodeBody(c *httpContext, into model.Decodable) error {
	ct := c.GetHeader("Content-Type")
	if idx := strings.IndexByte(ct, ';'); idx != -1 {
		ct = ct[:idx]
	}
	ct = strings.TrimSpace(ct)

	if strings.EqualFold(ct, contentTypeFormUrlEncoded) {
		vals, err := url.ParseQuery(string(c.Body()))
		if err != nil {
			return err
		}
		into.DecodeFields(&formReader{values: vals})
		return nil
	}

	return json.Decode(c.Body(), into)
}
