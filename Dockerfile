FROM golang:1.24-alpine

WORKDIR /app

COPY main.go .

RUN go build -o app main.go

EXPOSE 6778

CMD ["./app"]