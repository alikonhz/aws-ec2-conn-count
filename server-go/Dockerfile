FROM golang:1.21.0-bullseye as builder

COPY . /workdir
WORKDIR /workdir

RUN go build -o ./app -ldflags "-s -w" application.go

FROM gcr.io/distroless/base-debian11:nonroot
COPY --from=builder /workdir/app /bin/app

# noroot user from distroless image
USER 65532

EXPOSE 8080

ENTRYPOINT ["/bin/app"]