#!/usr/bin/env bash

startContainers() {
    pushd ../payment-processor > /dev/null
        docker compose up --build -d 1> /dev/null 2>&1
    popd > /dev/null
    pushd .. > /dev/null
        services=$(docker compose config --services | wc -l)
        echo "" > docker-compose-final.logs
        nohup docker compose up --build >> docker-compose-final.logs &
    popd > /dev/null
}

stopContainers() {
    pushd ..
        docker compose down -v --remove-orphans
        docker compose rm -s -v -f
    popd > /dev/null
    pushd ../payment-processor > /dev/null
        docker compose down --volumes > /dev/null
    popd > /dev/null
}

MAX_REQUESTS=550

start=$(date)

directory=.
participant=igorMSoares
echo ""
echo ""
echo "========================================"
echo "  Participant $participant starting..."
echo "========================================"

testedFile="$directory/final-results.json"

touch $testedFile
echo "executing test for $participant..."
stopContainers
startContainers

success=1
max_attempts=15
attempt=1
while [ $success -ne 0 ] && [ $max_attempts -ge $attempt ]; do
    curl -f -s --max-time 3 localhost:9999/payments-summary
    success=$?
    echo "tried $attempt out of $max_attempts..."
    sleep 5
    ((attempt++))
done

if [ $success -eq 0 ]; then
    echo "" > $directory/k6-final.logs
    k6 run -e MAX_REQUESTS=$MAX_REQUESTS -e TOKEN=$(uuidgen) --log-output=file=$directory/k6-final.logs rinha.js
    stopContainers
    echo "======================================="
    echo "working on $participant"
    sed -i '1001,$d' $directory/docker-compose-final.logs
    sed -i '1001,$d' $directory/k6-final.logs
    echo "log truncated at line 1000" >> $directory/docker-compose-final.logs
    echo "log truncated at line 1000" >> $directory/k6-final.logs
else
    stopContainers
    echo "[$(date)] Seu backend não respondeu nenhuma das $max_attempts tentativas de GET para http://localhost:9999/payments-summary. Teste abortado." > $directory/error-final.logs
    echo "[$(date)] Inspecione o arquivo docker-compose-final.logs para mais informações." >> $directory/error-final.logs
    echo "Could not get a successful response from backend... aborting test for $participant"
fi

echo "================================="
echo "  Finished testing $participant!"
echo "================================="

sleep 5
