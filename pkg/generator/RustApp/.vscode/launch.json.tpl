{
    // Debugging requires the CodeLLDB extension (vadimcn.vscode-lldb).
    // For more information, visit: https://go.microsoft.com/fwlink/?linkid=830387
    "version": "0.2.0",
    "configurations": [
        {
            "name": "App",
            "type": "lldb",
            "request": "launch",
            "cargo": {
                "args": ["build", "--bin=app"]
            },
            "env": {
                "BASE_PATH": "/{{ .InstanceInformation.CellName }}/{{ .PluginName }}",
                "LOG_LEVEL": "debug",
                "NOVA_API": "{{ .InstanceInformation.NovaApiHost }}",
                "CELL_NAME": "{{ .InstanceInformation.CellName }}",
                "ACCESS_TOKEN": "{{ .InstanceInformation.AccessToken }}"
            }
        }
    ]
}
