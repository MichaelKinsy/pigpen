# Hermetic deterministic mock model server + MCP server. Build: docker build -t kagent-mock .
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mockllm .

FROM scratch
COPY --from=build /out/mockllm /mockllm
USER 65532:65532
EXPOSE 8080 8081
ENTRYPOINT ["/mockllm"]
