// Process/API health, deliberately independent of the initial static build.
(async () => {
  const response = await fetch("http://127.0.0.1:" + (process.env.PORT || "8080") + "/rest/v1", {
    signal: AbortSignal.timeout(4000),
    redirect: "error",
  });
  if (!response.ok || (await response.json()).version !== "v1") {
    throw new Error("Aigoni API is not healthy");
  }
})().catch((error) => {
  console.error(error.message);
  process.exitCode = 1;
});
