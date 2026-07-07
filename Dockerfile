FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . ./

ARG TARGETOS
ARG TARGETARCH

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /out/minirouter ./cmd/minirouter

FROM gcr.io/distroless/static:nonroot

WORKDIR /
COPY --from=build /out/minirouter /minirouter

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/minirouter"]