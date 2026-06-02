FROM alpine:latest
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY bin/service .
EXPOSE 8080
CMD ["./service"]
