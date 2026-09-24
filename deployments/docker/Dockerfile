FROM golang:1.26-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/workaholic-server ./cmd/server \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/workaholic-cli ./cmd/cli

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/workaholic-server /workaholic-server
COPY --from=build /out/workaholic-cli /workaholic-cli
COPY runbooks /runbooks
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/workaholic-server"]
