#!/bin/bash

# curl -s -H 'Accept: application/json' https://aquila.red/all/packages | jq | less

curl -s -H 'Accept: application/json' https://aquila.red/1/vrischmann/prometheus | jq | less