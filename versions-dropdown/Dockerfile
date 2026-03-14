FROM golang:1.26.1-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /action .

FROM alpine:3.23.3
RUN apk --no-cache add ca-certificates git gnupg
COPY --from=build /action /action
ENTRYPOINT ["/action"]
