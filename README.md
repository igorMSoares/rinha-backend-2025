# Rinha de Backend 2025

Sistema de encaminhamento de pagamentos para a minha solução da [Rinha de Backend 2025](https://github.com/zanfranceschi/rinha-de-backend-2025).

## Arquitetura

<img width="1489" height="1168" alt="arquitetura-rinha-2025" src="https://github.com/user-attachments/assets/ef71912e-6b79-4c75-81f4-ef8954eda812" />


## Serviços

- `payment-proxy`: responsável por processar as requisições `POST /payments` e `GET /payments-summary`.
- `nginx`: responsável por balancear as requisições entre as 2 instâncias de `payment-proxy`.
- `redis`: utilizado como fila para os pagamentos a serem encaminhados para os `payment-processor` e também como storage para registrar o total de requisições e o valor total processados por cada `payment-processor`.

## Descrição

### Payment Proxy

O sistema de encaminhamento de pagamentos roda 2 instâncias do serviço `payment-proxy`, implementado em Go.

Cada payment proxy é composto por:

- Servidor http para tratar as requisições  `POST /payments` e `GET /payments-summary`
- Workers para encaminhar os pagamentos para os _processors_ e registrar as transações bem sucedidas
- Load balancer para escolher qual instância de _processor_ encaminhar o pagamento
- Circuit breaker para desviar ou suspender requisições para um _processor_ indisponível
- Client http para enviar requisições para os `payment-processor`

#### Workers

Requisições `POST /payments` serão tratadas pelo web server do `payment-proxy` encaminhando-as para uma fila de processamento no redis.
Um `Work Dispatcher` está inscrito nessa fila e distribui o processamento entre os `Workers` disponíveis em sua worker pool.
Cada `Worker` possui um `Load Balancer` interno para distribuir as requisições entre as 2 instâncias de `payment-processor`.

#### Load Balancer Interno

O `Load Balancer` utiliza uma estratégia de [Thompson sampling](https://en.wikipedia.org/wiki/Thompson_sampling) com distribuições beta para escolher para qual _processor_ (`default` ou `fallback`) encaminhar as requisições, levando em consideração a latência de cada `payment-processor` e o custo mais elevado do fallback. O balancer também utiliza um `Circuit Breaker` para cada instância de `payment-processor`, desviando ou suspendendo o fluxo das requisições quando algum _processor_ passa por instabilidades.

#### Registrando o Summary

Após um `payment-processor` retornar sucesso, o `Worker` registra o valor processado no `redis` e atualiza a contagem do total de processamentos.
No `redis` os valores e contagem são atrelados ao timestamp da requisição, permitindo consultar o total de requisições e o valor total dos pagamentos processados em um determinado período de tempo. Estes valores são retornados na resposta das requisições `GET /payments-summary`.

## Configurações

Arquivo `.env` de referência:

```bash
## Usa uma única thread do sistema operacional para evitar constantes mudanças de contexto
GOMAXPROCS=1

## Pool de conexões do Redis
DISPATCHER_REDIS_POOL=60

### LOAD BALANCER ###
## Mais perto de 1.0 maior é a penalidade pelo custo
COST_WEIGHT=0.25

## Score do load balancer penaliza a latencia em relação a este limite
LATENCY_LIMIT=100ms

## Duração que o LB fica suspenso caso não haja payment-processor disponível
LB_CIRCUIT_TIMEOUT=1.5s
####################

### CIRCUIT BREAKER ###
## Duração que o circuito fica aberto até transicionar para half-open
CB_RECOVERY_TIMEOUT=0.5s

## Qtd de sucessos para transicionar de half-open para closed
CB_RECOVERY_ATTEMPTS=50

## Qtd de falhas para um circuito fechado abrir
CB_FAILURE_THRESHOLD=5
####################

## Timeout das requisições para os payment-processor
PROCESSOR_REQ_TIMEOUT=10s

## Qtd total de workers
MAX_WORKERS=15
```

## Testes

Para executar o teste parcial (divulgado antes de encerrar o período de submissão):

```bash
# Estando na raiz do projeto
cd rinha-test

./run-partial.sh
```

Para executar o teste final:

```bash
# Estando na raiz do projeto
cd rinha-test

./run-final.sh
```

Os resultados serão salvos em `./rinha-test/partial-results.json` e `./rinha-test/final-results.json`
