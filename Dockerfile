FROM golang:1.26-alpine AS build
WORKDIR /src
COPY src/go.mod ./
RUN go mod download
COPY src/ .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gateway .

FROM gcr.io/distroless/static:nonroot AS final
COPY --from=build /out/gateway /gateway
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/gateway"]
