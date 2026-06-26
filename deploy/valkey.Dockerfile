# Path 2 image: single-driver binary (only the valkey driver is compiled in).
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download           # rueidis (M3) is now a real dependency — verified via go.sum
COPY . .
RUN CGO_ENABLED=0 go build -tags valkey -o /plq ./cmd/plq

FROM gcr.io/distroless/static-debian12
COPY --from=build /plq /plq
ENTRYPOINT ["/plq"]
CMD ["worker"]
