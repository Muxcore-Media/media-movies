FROM golang:1.26-alpine AS builder
COPY core/ /build/core/
COPY contracts-media-admin/ /build/contracts-media-admin/
COPY metadata-tmdb/ /build/metadata-tmdb/
COPY media-movies/ /build/media-movies/
WORKDIR /build/media-movies
RUN go mod download
RUN CGO_ENABLED=0 go build -o /media-movies ./cmd/module
FROM gcr.io/distroless/static-debian12
COPY --from=builder /media-movies /
ENTRYPOINT ["/media-movies"]
