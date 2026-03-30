#!/usr/bin/env node

const fs = require("fs");
const path = require("path");
const { spawn } = require("child_process");

const packageRoot = path.resolve(__dirname, "..");
const runtimeBinary = path.join(
  packageRoot,
  "runtime",
  process.platform === "win32" ? "cccli.exe" : "cccli"
);
const binaryPath = process.env.CCCLI_BINARY_PATH || runtimeBinary;

if (!fs.existsSync(binaryPath)) {
  const installerPath = path.join(packageRoot, "scripts", "install.js");
  const message = process.env.CCCLI_BINARY_PATH
    ? `CCCLI_BINARY_PATH points to a missing file: ${process.env.CCCLI_BINARY_PATH}`
    : `cccli binary is missing. Reinstall the package or run "node ${installerPath}".`;

  console.error(message);
  process.exit(1);
}

const child = spawn(binaryPath, process.argv.slice(2), {
  stdio: "inherit"
});

child.on("exit", (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }

  process.exit(code === null ? 1 : code);
});

child.on("error", (error) => {
  console.error(`Failed to launch cccli: ${error.message}`);
  process.exit(1);
});

