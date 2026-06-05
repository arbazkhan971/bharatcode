When to call this tool

Use `web_search` when BharatCode needs to discover current public web pages for
a query before choosing a URL to fetch. It returns concise search results rather
than full page bodies.

Arguments:

- `query` string, required: natural-language or keyword search query.
- `allowed_domains` string array, optional: keep only results whose host is (or
  is a subdomain of) one of these domains, e.g. `["go.dev", "pkg.go.dev"]`.
- `blocked_domains` string array, optional: drop results whose host is (or is a
  subdomain of) one of these domains. A blocked domain always wins over an
  allowed one. Domains may be given with or without a scheme or leading `www.`.

What success looks like:

The result contains up to five numbered results. Each result includes a title,
URL, and snippet when the provider includes one, giving the model enough context
to decide whether a follow-up `web_fetch` call is useful.

Failure cases:

Malformed JSON, an empty query, provider HTTP errors, or malformed provider
configuration returns an error result. If the provider returns no usable results,
BharatCode reports that no search results were found.
