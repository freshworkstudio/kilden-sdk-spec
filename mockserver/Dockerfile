FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /mockserver .

FROM scratch
COPY --from=build /mockserver /mockserver
EXPOSE 8090
ENTRYPOINT ["/mockserver"]
