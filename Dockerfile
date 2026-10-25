FROM golang:1.27.2-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/auth ./cmd/auth
RUN CGO_ENABLED=0 go build -trimpath -o /out/migrate-auth ./cmd/migrate-auth

FROM alpine:3.22
RUN apk add --no-cache ca-certificates
COPY --from=build /out/auth /usr/local/bin/auth
COPY --from=build /out/migrate-auth /usr/local/bin/migrate-auth
USER 65532:65532
EXPOSE 3067
ENTRYPOINT ["/usr/local/bin/auth"]
