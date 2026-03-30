#!/usr/bin/env node

const fs = require("fs");
const https = require("https");
const path = require("path");
const { spawnSync } = require("child_process");

const packageRoot = path.resolve(__dirname, "..");
const packageJson = JSON.parse(
  fs.readFileSync(path.join(packageRoot, "package.json"), "utf8")
);

const binaryName = packageJson.config.binaryName || "cccli";
const version = process.env.CCCLI_VERSION || packageJson.version;
const skipDownload = process.env.CCCLI_SKIP_DOWNLOAD === "1";
const explicitUrl = process.env.CCCLI_DOWNLOAD_URL;
const repo = process.env.CCCLI_GITHUB_REPO || packageJson.config.githubRepo;
const tag = version.startsWith("v") ? version : `v${version}`;
const normalizedVersion = tag.replace(/^v/, "");
const runtimeDir = path.join(packageRoot, "runtime");
const archiveDir = path.join(packageRoot, "runtime-download");

const platformMap = {
  linux: "linux",
  darwin: "darwin",
  win32: "windows"
};

const archMap = {
  x64: "amd64",
  arm64: "arm64"
};

function log(message) {
  process.stdout.write(`[cccli] ${message}\n`);
}

function fail(message) {
  process.stderr.write(`[cccli] ${message}\n`);
  process.exit(1);
}

function isDevelopmentVersion(currentVersion) {
  return /dev|snapshot|nightly/i.test(currentVersion);
}

function resolvePlatform() {
  const platform = platformMap[process.platform];
  const arch = archMap[process.arch];

  if (!platform) {
    fail(`Unsupported platform: ${process.platform}`);
  }

  if (!arch) {
    fail(`Unsupported architecture: ${process.arch}`);
  }

  return { platform, arch };
}

function assetInfo() {
  const { platform, arch } = resolvePlatform();
  const extension = platform === "windows" ? "zip" : "tar.gz";
  const archiveName = `${binaryName}_${normalizedVersion}_${platform}_${arch}.${extension}`;
  const baseUrl =
    process.env.CCCLI_DOWNLOAD_BASE_URL ||
    `https://github.com/${repo}/releases/download/${tag}`;

  return {
    archiveName,
    archivePath: path.join(archiveDir, archiveName),
    downloadUrl: explicitUrl || `${baseUrl}/${archiveName}`,
    extension,
    extractedBinary: path.join(
      runtimeDir,
      platform === "windows" ? `${binaryName}.exe` : binaryName
    )
  };
}

function ensureCleanDir(dir) {
  fs.rmSync(dir, { recursive: true, force: true });
  fs.mkdirSync(dir, { recursive: true });
}

function download(url, destination, redirects = 5) {
  return new Promise((resolve, reject) => {
    const request = https.get(url, (response) => {
      if (
        response.statusCode &&
        response.statusCode >= 300 &&
        response.statusCode < 400 &&
        response.headers.location
      ) {
        response.resume();

        if (redirects === 0) {
          reject(new Error(`Too many redirects while downloading ${url}`));
          return;
        }

        const redirected = new URL(response.headers.location, url).toString();
        download(redirected, destination, redirects - 1)
          .then(resolve)
          .catch(reject);
        return;
      }

      if (response.statusCode !== 200) {
        response.resume();
        reject(
          new Error(`Download failed for ${url}: HTTP ${response.statusCode}`)
        );
        return;
      }

      const file = fs.createWriteStream(destination);
      response.pipe(file);

      file.on("finish", () => {
        file.close(resolve);
      });

      file.on("error", (error) => {
        fs.rmSync(destination, { force: true });
        reject(error);
      });
    });

    request.on("error", (error) => {
      fs.rmSync(destination, { force: true });
      reject(error);
    });
  });
}

function runCommand(command, args) {
  const result = spawnSync(command, args, {
    stdio: "inherit",
    cwd: packageRoot
  });

  if (result.status !== 0) {
    fail(`${command} ${args.join(" ")} failed with exit code ${result.status}`);
  }
}

function extractArchive(info) {
  ensureCleanDir(runtimeDir);

  if (info.extension === "zip") {
    runCommand("powershell.exe", [
      "-NoProfile",
      "-NonInteractive",
      "-Command",
      `Expand-Archive -LiteralPath '${info.archivePath.replace(/'/g, "''")}' -DestinationPath '${runtimeDir.replace(/'/g, "''")}' -Force`
    ]);
    return;
  }

  runCommand("tar", ["-xzf", info.archivePath, "-C", runtimeDir]);
}

function ensureExecutable(binaryPath) {
  if (process.platform !== "win32") {
    fs.chmodSync(binaryPath, 0o755);
  }
}

async function main() {
  if (skipDownload) {
    log("Skipping binary download because CCCLI_SKIP_DOWNLOAD=1.");
    return;
  }

  if (!explicitUrl && isDevelopmentVersion(version)) {
    log(
      `Skipping binary download for development version ${version}. Set CCCLI_VERSION or CCCLI_DOWNLOAD_URL to test downloads locally.`
    );
    return;
  }

  const info = assetInfo();
  ensureCleanDir(archiveDir);

  log(`Downloading ${info.downloadUrl}`);
  await download(info.downloadUrl, info.archivePath);
  extractArchive(info);

  if (!fs.existsSync(info.extractedBinary)) {
    fail(`Binary not found after extraction: ${info.extractedBinary}`);
  }

  ensureExecutable(info.extractedBinary);
  fs.rmSync(archiveDir, { recursive: true, force: true });
  log(`Installed ${path.basename(info.extractedBinary)}`);
}

main().catch((error) => {
  fail(error.message);
});

