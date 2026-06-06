When to call this tool

Use `web_fetch` when BharatCode needs the contents of a specific HTTP or HTTPS
page that the user has asked about or supplied as a source. It fetches the URL
and converts simple HTML into model-readable markdown-like text.

Only HTML responses are reduced to markdown. A response the server labels as
JSON, JavaScript, CSS, XML, or another non-HTML type is returned verbatim, so
raw source, config files, and API payloads keep their angle brackets (generics
like `Vec<T>`, a `chan<-`, a shell redirect, or a `<` inside a JSON string)
instead of having them stripped as if they were tags. When the server omits the
content type, the body is rendered as markdown only if it actually looks like
HTML.

Arguments:

- `url` string, required: absolute URL beginning with `http://` or `https://`.
- `prompt` string, optional: short note about what information matters on the page.

What success looks like:

The result contains readable page text with headings, link text, and list items.
Code in `<pre>` blocks is preserved verbatim inside fenced code blocks (its
indentation and line breaks intact), and inline `<code>` is wrapped in backticks,
so documentation examples survive instead of being flattened. Script and style
content is stripped before the text is returned to the model.

Failure cases:

Malformed JSON, a missing URL, a non-HTTP scheme, a non-2xx response, or a
network failure returns an error. Large responses are capped so BharatCode tool
output stays bounded.

For safety, `web_fetch` refuses to connect to non-public addresses: private
networks, link-local hosts (including the `169.254.169.254` cloud-metadata
service), and multicast addresses are blocked, and the block is re-checked on
every redirect. Loopback (`localhost`) is allowed so local development servers
remain reachable.
