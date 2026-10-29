#!/bin/sh

# Function to check if an app is reachable
# Usage: check_app_reachability app_uri [timeout] [interval]
check_app_reachability() {
  uri=$1
  timeout=${2:-300}  # Default timeout: 300 seconds
  interval=${3:-10}  # Default interval: 10 seconds
  
  elapsed=0
  while [ $elapsed -lt $timeout ]; do
    status=$(curl -L -o /dev/null -s -w "%{http_code}\n" -H "Authorization: Bearer $INSTANCE_TOKEN" "https://$INSTANCE_HOST/cell/$uri")
    if [ $status -eq 200 ]; then
      echo "App $uri is reachable"
      return 0
    else 
      echo "App $uri is not ready (HTTP status: $status)"
    fi
    echo "Waiting for app $uri to be reachable..."
    sleep $interval
    elapsed=$((elapsed + interval))
  done
  echo "App $uri is not reachable after $timeout seconds"
  return 1
}
