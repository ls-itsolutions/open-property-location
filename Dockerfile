# syntax=docker/dockerfile:1

# Binary only:
#   docker build --target app -t opl .
# Binary with data (expects places.db in out/):
#   docker build --target with-data --build-context data=out -t opl:germany .

FROM --platform=$BUILDPLATFORM golang:1.27 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/opl ./cmd/opl

FROM gcr.io/distroless/static-debian13:nonroot AS app
LABEL org.opencontainers.image.source="https://github.com/ls-itsolutions/open-property-location" \
      org.opencontainers.image.description="Structured surroundings of a location from OpenStreetMap data" \
      org.opencontainers.image.licenses="MIT"
COPY --from=build /out/opl /opl
ENV OPL_DATA=/data/places.db \
    OPL_ADDR=:8080
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s CMD ["/opl", "healthcheck"]
ENTRYPOINT ["/opl"]
CMD ["serve"]

FROM app AS with-data
LABEL org.opencontainers.image.licenses="MIT AND ODbL-1.0"
COPY LICENSE-DATA /LICENSE-DATA
COPY --from=data places.db /data/places.db
