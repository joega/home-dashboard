# syntax=docker/dockerfile:1
# Base indexes pinned deliberately; review updates rather than using a floating latest.
FROM --platform=$BUILDPLATFORM golang:1.27.2-trixie@sha256:47667934874913a449b96faa7ef4777fa285d9ec29126ee80ed61c45b7cccf0e AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
# Tests run on the build host; the executable is built for the requested target.
RUN CGO_ENABLED=0 go test ./... && go vet ./...
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/home-dashboard ./cmd/home-dashboard
RUN install -d -m 0750 -o 65532 -g 65532 /out/data && \
    touch /out/data/.keep && chown 65532:65532 /out/data/.keep

FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
COPY --from=build /out/home-dashboard /usr/local/bin/home-dashboard
COPY --from=build --chown=65532:65532 /out/data /data
COPY LICENSE THIRD_PARTY_NOTICES.md /usr/share/licenses/home-hq/
USER 65532:65532
WORKDIR /data
ENV DASHBOARD_ADDR=0.0.0.0:4173 \
    DASHBOARD_DB=/data/dashboard.sqlite \
    DASHBOARD_TIMEZONE=America/New_York
EXPOSE 4173
ENTRYPOINT ["/usr/local/bin/home-dashboard"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
    CMD ["/usr/local/bin/home-dashboard", "-healthcheck"]
