# Rinha de Backend 2025

Um sistema de encaminhamento de pagamentos para a minha solução da [Rinha de Backend 2025](https://github.com/zanfranceschi/rinha-de-backend-2025).

## Serviços

- `payment-proxy`: responsável por processar as requisições que chegam (`POST /payments` e `GET /payments-summary`).
- `nginx`: responsável por balancear as requisições entre as 2 instâncias de `payment-proxy`.
- `redis`: utilizado como fila para os pagamentos a serem encaminhados para os `payment-processor` e também como storage para registrar o total de requisições e o valor total processados por cada `payment-processor`.

## Descrição

O sistema de encaminhamento de pagamentos roda 2 instâncias desse serviço, implementado em Go.

Requisições `POST /payments` serão tratadas pelo web server do `payment-proxy` encaminhando-as para uma fila de processamento (`work_queue`) no redis.
Um `Work Dispatcher` está inscrito nessa fila e distribui o processamento entre os `Workers` disponíveis em suas worker pool.

Cada `Worker` possui um `Load Balancer` interno para distribuir as requisições entre as 2 instâncias de `payment-processor`.
O `Load Balancer` utiliza uma estratégia de [Thompson sampling](https://en.wikipedia.org/wiki/Thompson_sampling) com distribuições beta para escolher para qual instância (`default` ou `fallback`) encaminhar as requisições levando em consideração a latência e o custo de cada `payment-processor`. O balancer também utiliza um `Circuit Breaker` para cada instância de `payment-processor`, desviando o fluxo das requisições quando algum processor passa por instabilidades.

Após o `payment-processor` retornar sucesso, o `Worker` registra o valor processado no `redis` e também a contagem do total de processamento.
No `redis` os valores e contagem são atrelados ao timestamp da requisição, permitindo consultar o total de requisições e o valor total dos pagamentos processados em um determinado período de tempo. Estes valores são retornados na resposta das requisições `GET /payments-summary`.

---

_(Assim que possível vou melhorando esse README com a descrição do projeto e incluindo mais detalhes.)_
