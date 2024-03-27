FROM golang:1.22.1-alpine3.19 as builder

WORKDIR /app

ENV USER=appuser
ENV UID=10001 

RUN apk update

RUN adduser \
  --disabled-password \
  --gecos "" \
  --home "/nonexistent" \
  --shell "/sbin/nologin" \
  --no-create-home \
  --uid "${UID}" \
  "${USER}"

COPY go.mod go.sum ./

RUN go mod download

COPY gen gen
COPY main.go .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-w -s" -o snowflaker .

FROM scratch

COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group
COPY --from=builder /app/snowflaker .

USER appuser:appuser

ENTRYPOINT ["/snowflaker"]

