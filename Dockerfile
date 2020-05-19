FROM golang:alpine AS build-env
RUN apk --no-cache add ca-certificates

# Dependencies
WORKDIR /build
ENV GO111MODULE=on
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download

# Build
COPY . ./
# TEST
RUN go test ./...

# BUILD
RUN go build -ldflags '-w -s' -o /app ./cmd/server

# Build runtime container
FROM scratch
COPY --from=build-env /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build-env /app /app

USER 1212
EXPOSE 5100

ENTRYPOINT [ "/app" ]
