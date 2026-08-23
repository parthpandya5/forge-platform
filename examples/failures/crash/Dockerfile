FROM alpine:3.21
RUN adduser -D -u 10001 app
USER app
CMD ["sh", "-c", "echo intentional startup failure; exit 42"]
