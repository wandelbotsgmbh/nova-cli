#!/bin/bash
# Manual smoke test for "nova nucleus" against the configured instance.
set -e
nova nucleus ls
nova nucleus mkdir /Projects/testcli
nova nucleus ls
nova nucleus cp README.md n:/Projects/testcli/README.md
nova nucleus ls /Projects/testcli/
nova nucleus cp n:/Projects/testcli/ n:/Projects/testcli2/
nova nucleus ls /Projects/
nova nucleus ls /Projects/testcli2/
nova nucleus rm /Projects/testcli/README.md
nova nucleus ls /Projects/testcli/
nova nucleus rm /Projects/testcli/
nova nucleus rm -r /Projects/testcli2/
