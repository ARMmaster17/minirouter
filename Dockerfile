FROM golang:1.24-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/minirouter ./cmd/minirouter

FROM gcr.io/distroless/static:nonroot

WORKDIR /
COPY --from=build /out/minirouter /minirouter

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/minirouter"]