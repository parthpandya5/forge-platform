import express from "express";

const app = express();
const port = Number(process.env.PORT ?? 3000);

app.get("/", (_request, response) => {
  response.json({ message: "hello from Forge", runtime: "node" });
});

app.get("/health", (_request, response) => {
  response.json({ status: "ok" });
});

app.listen(port, "0.0.0.0", () => {
  console.log(`hello-node listening on ${port}`);
});
