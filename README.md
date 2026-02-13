# thin

thin is a lightweight CLI that executes **provider-native tools**.

It provides a stable execution layer while all tool logic, workflows,
and behavior live in **provider-owned binaries**.

thin is intentionally minimal and designed to remain unchanged
as providers evolve independently.

---

## Core Concepts

- **Provider**  
  A versioned distribution of tools (binaries) that implement tool behavior.

- **Tool**  
  A provider-supplied binary (e.g. `ci`, `deploy`).

- **Capability**  
  A subcommand of a tool (e.g. `ci plan`, `ci run`).

thin never understands capabilities — it only executes tools.

---

## Directory Model

thin uses two homes:

### Global (shared tools)

```
~/.thin/providers/<namespace>/<provider>/<version>/tools/<tool>
```

### Project-local (execution context)

```
./.thin/
└── active-provider.yaml
```

This mirrors Terraform's global vs working-directory split.

---

## Active Provider

At any time, exactly one provider is active.

Set it with:

```bash
thin provider use sourceplane/beeflock-k8s@v0.1.0
```

This avoids tool name collisions across providers.

---

## Installation

### Quick Install (Recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/sourceplane/thin/main/install.sh | bash
```

This script automatically detects your OS and architecture, downloads the latest release, and installs it to `/usr/local/bin`.

### Homebrew

```bash
brew install sourceplane/tap/thin
```

### Manual Release Download

Download the latest release for your platform:

```bash
# macOS (Apple Silicon)
curl -L https://github.com/sourceplane/thin/releases/latest/download/thin_darwin_arm64.tar.gz | tar xz

# macOS (Intel)
curl -L https://github.com/sourceplane/thin/releases/latest/download/thin_darwin_x86_64.tar.gz | tar xz

# Linux (x86_64)
curl -L https://github.com/sourceplane/thin/releases/latest/download/thin_linux_x86_64.tar.gz | tar xz

# Linux (ARM64)
curl -L https://github.com/sourceplane/thin/releases/latest/download/thin_linux_arm64.tar.gz | tar xz
```

Move the binary to your PATH:

```bash
sudo mv thin /usr/local/bin/
chmod +x /usr/local/bin/thin
```

### From Source

```bash
git clone https://github.com/sourceplane/thin.git
cd thin
go build -o thin .
sudo mv thin /usr/local/bin/
```

---

## Usage

```bash
thin tools
thin ci plan --env prod
thin ci run
```

Execution mapping:

```
thin ci plan
→ exec ~/.thin/providers/.../tools/ci plan
```

Arguments, stdin, stdout, stderr, and exit codes are passed through unchanged.

---

## What thin does NOT do

* No provider installation (yet)
* No YAML or JSON parsing
* No CI logic
* No planning logic
* No plugin SDK
* No RPC or IPC

thin is an execution substrate — not a framework.

---

## Inspiration

* kubectl plugin execution model
* Terraform provider lifecycle
* Unix process composition

---

## Philosophy

> Tool logic should be versioned, reusable, and owned by providers — not copied into repositories.

thin exists to make that possible.
