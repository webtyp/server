| Name | rule |
|---|---|
| [TestHTTPContext_PathAndRawQuery](../httpd/adapter_test.go) | a request POST /api/q?a=1&b=two through the adapter: ctx.Path() is /api/q, ctx.RawQuery() is a=1&b=two; without query RawQuery() is "" |
