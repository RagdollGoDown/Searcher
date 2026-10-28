# Deployment

The Compose deployment puts Caddy in front of Searcher. Caddy obtains and
renews the TLS certificate for `DOMAIN`; Searcher and Ollama are only reachable
on the private Docker network.

## Initial setup

1. Point the domain's DNS A/AAAA record at the server.
2. Copy `.env.example` to `.env`.
3. Set `DOMAIN` and `BASIC_AUTH_USER`.
4. Generate a password hash:

   ```sh
   docker run --rm caddy:2-alpine \
     caddy hash-password --plaintext 'choose-a-long-password'
   ```

5. Put the generated value in `BASIC_AUTH_HASH`.
6. Allow TCP ports 80 and 443 through the server firewall.
7. Start the stack:

   ```sh
   docker compose up -d
   ```

The site will be available at `https://DOMAIN`. Do not publish Ollama's port
11434 or Searcher's port 8080 to the Internet.

## Shared authentication and limits

The Basic Auth credential is shared rather than tied to individual users. It
protects the application, but it does not provide per-user audit trails or
per-user quotas. Reverse-proxy rate limiting can reduce anonymous abuse, but a
meaningful per-session limit requires Searcher to issue and track sessions.
Management endpoints should remain behind authentication even if rate limiting
is added.
