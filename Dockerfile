FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -o /out/spurstack ./cmd/spurstack

FROM debian:bookworm-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates git openssh-client \
  && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/spurstack /usr/local/bin/spurstack
ENV WORKSPACE_DIR=/var/lib/spurstack/workspace \
    OPENAI_BASE_URL=https://openrouter.ai/api/v1 \
    SPUR_MODEL=anthropic/claude-sonnet-4.6 \
    SPUR_LABEL=agent-ready \
    POLL_INTERVAL=60s
VOLUME ["/var/lib/spurstack/workspace"]
ENTRYPOINT ["/usr/local/bin/spurstack"]
