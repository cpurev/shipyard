# Static shipyard binary, non-root, no shell in the final image.
# The process serves HTTP on $PORT and does not call a cloud API unless
# SHIPYARD_PROVIDER is pointed at a cluster you already run.

FROM golang:1.27.4-alpine AS build

WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/shipyard ./cmd/shipyard
RUN addgroup -g 65532 -S shipyard \
    && adduser -D -H -u 65532 -G shipyard -S shipyard \
    && mkdir -p /data/shipyard \
    && touch /data/shipyard/.keep \
    && chown -R 65532:65532 /data/shipyard

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /etc/passwd /etc/passwd
COPY --from=build /etc/group /etc/group
COPY --from=build --chown=65532:65532 /data/shipyard /var/lib/shipyard
COPY --from=build /out/shipyard /shipyard
USER 65532:65532
ENV PORT=8080 \
    SHIPYARD_HOME=/var/lib/shipyard \
    SHIPYARD_PROVIDER=fake
EXPOSE 8080
ENTRYPOINT ["/shipyard"]
CMD ["serve"]
