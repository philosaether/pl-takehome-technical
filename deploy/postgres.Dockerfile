# Path 1 image: single-driver binary (only the postgres driver is compiled in).
FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod ./
# go.sum is added when the first dependency lands (M1: pgx).
RUN go mod download || true
COPY . .
RUN CGO_ENABLED=0 go build -tags postgres -o /plq ./cmd/plq

FROM gcr.io/distroless/static-debian12
COPY --from=build /plq /plq
ENTRYPOINT ["/plq"]
CMD ["worker"]
