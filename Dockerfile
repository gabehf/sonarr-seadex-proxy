FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
ARG ARCH=amd64
WORKDIR /build
COPY main.go .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${ARCH} go build -o seadex-proxy main.go

FROM scratch
COPY --from=build /build/seadex-proxy /seadex-proxy
ENTRYPOINT ["/seadex-proxy"]
EXPOSE 6778
