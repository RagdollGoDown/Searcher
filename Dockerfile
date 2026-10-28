FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/searcher .

FROM debian:bookworm-slim

RUN groupadd --system searcher && \
    useradd --system --gid searcher --home-dir /app --no-create-home searcher

WORKDIR /app
COPY --from=build /out/searcher /app/searcher
RUN mkdir /app/data && chown searcher:searcher /app/data

USER searcher
EXPOSE 8080
ENTRYPOINT ["/app/searcher"]
