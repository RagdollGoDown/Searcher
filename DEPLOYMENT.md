# Deployment

The Compose deployment puts Caddy in front of Searcher. Caddy obtains and
renews the TLS certificate for `DOMAIN`, adds baseline security headers,
limits request bodies to 25 MB, and applies IP-based rate limits. Searcher and
Ollama are only reachable on the private Docker network.

## Initial setup

1. Point the domain's DNS A/AAAA record at the server.
2. Copy `.env.example` to `.env`.
3. Set `DOMAIN`.
4. Allow TCP ports 80 and 443 through the server firewall.
5. Start the stack:

   ```sh
   docker compose up -d
   ```

The site will be available at `https://DOMAIN`. Do not publish Ollama's port
11434 or Searcher's port 8080 to the Internet.

This branch does not add authentication. It provides transport security,
private service networking, security headers, request-size protection, and
coarse IP-based anti-abuse limits:

- 60 requests per minute per IP overall
- 10 chat requests per minute per IP
- 5 uploads per 10 minutes per IP
- 2 crawl starts per 10 minutes per IP

The limits are enforced by the custom Caddy image in `caddy/Dockerfile`,
which includes `github.com/mholt/caddy-ratelimit`. They are not per-user
quotas: users behind the same NAT share a bucket, and a client can potentially
change IP addresses. The limits are in-memory and reset when Caddy restarts.

This is not sufficient for a public multi-user deployment: the application
APIs remain reachable to anyone who can access the domain. Add authentication
before deploying this branch as a public service.
