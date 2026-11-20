# Branches públicas e publicação

Repositório: `git@github.com:gr1ksdev/UnoKamikaze.git`
(GitHub: `gr1ksdev/UnoKamikaze`). Módulo Go: `github.com/gr1ksdev/UnoKamikaze`.

A linha de produto é `v3`: bot de Telegram puro, sem Mini App, API REST ou
WebSocket. É a **branch padrão** deste repositório e a única mantida aqui.

## Branches

| Branch | Conteúdo |
|---|---|
| `v3` | Produto atual: bot de Telegram puro, modo Caseiro único. Branch padrão. |

As branches `main` e `dev` pertencem ao repositório anterior
(`gr1ksdev/UnoBotGO`) e preservam o histórico do v2, incluindo a Mini App. A
migração da `v3` preservou o histórico completo, de modo que os commits do v2
continuam na ancestralidade da `v3`, mas este repositório não mantém branches
`main` ou `dev`.

## Fluxo

1. Desenvolva e faça commits na `v3`.
2. O CI da branch (`v3-ci.yml`) executa testes, race detector, variantes
   `debugcards` e `integration`, vet, build e validação Docker, com PostgreSQL
   isolado.
3. A publicação da imagem é feita por `v3-container.yml` em push na `v3`,
   gerando `ghcr.io/gr1ksdev/unokamikaze:latest` e `:sha-<commit>`.
4. Cada tag é um manifesto OCI com variantes `linux/amd64` e `linux/arm64`.

Não faça merge das linhas do v2 na `v3`: a árvore do produto não deve receber
artefatos da Mini App nem o legado do V1.

## Remote antigo

Localmente, o remote `upstream` continua apontando para
`git@github.com:gr1ksdev/UnoBotGO.git` como referência do estado anterior. O
remote `origin` é este repositório.

O workflow `public-tree.yml` era a guarda de publicação da árvore limpa do v2
(disparava em `main`). Aqui ele permanece inativo, pois não existe branch
`main` neste repositório.
