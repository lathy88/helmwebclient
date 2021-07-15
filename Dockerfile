FROM golang:1.16-alpine

ENV GO111MODULE=on \
    CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

WORKDIR /app

COPY go.mod .
COPY go.sum .
RUN go mod download

COPY *.go .

RUN go build -o helm-web-client .

WORKDIR /opt/app/helmclient

RUN mkdir -p chart

RUN cp /app/helm-web-client .

EXPOSE 9090

CMD [ "/opt/app/helmclient/helm-web-client" ]