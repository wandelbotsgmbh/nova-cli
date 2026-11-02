---
mode: agent
model: Claude Sonnet 4
tools: ["runCommands", 'terminalSelection', 'terminalLastCommand']
---
Do local development setup for the Wandelbots Nova application, including installing dependencies, setting up the Python environment, and preparing the FastAPI server for development.

Follow these steps:
1- make sure nova cli is installed.
If it is not installed tell the user to visit this page and follow the guidelines there: https://github.com/wandelbotsgmbh/nova-cli

2- make sure uv is installed.
If uv is not installed, tell the user to use this command for Mac/Linux:
```bash
curl -LsSf https://astral.sh/uv/install.sh | sh
```

this command for windows:
```powershell
powershell -ExecutionPolicy ByPass -c "irm https://astral.sh/uv/install.ps1 | iex"
```

3- Create virtual environment
```bash
uv sync
```

4- Install dependencies
```bash
uv sync
```

5- Make sure the app can reach the NOVA instance
```bash
nova app update-env
```
This refreshes `NOVA_ACCESS_TOKEN` in the gitignored `.env` (portal tokens last 24h) and keeps the other values.
Check that the listed `NOVA_API` is the intended instance; if the user switched instances, run `nova app update-env --reset`.
Never print or commit the token.

6- Download robot models
```bash
uv run download-models
```