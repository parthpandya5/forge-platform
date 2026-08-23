FROM python:3.13-alpine
RUN adduser -D -u 10001 app
USER app
EXPOSE 8080
CMD ["python", "-m", "http.server", "8080"]
