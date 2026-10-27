## Melhorias

- Reutiliza downloads verificados de atualizações nativas após falhas de instalação, verificando novamente a integridade e removendo arquivos rejeitados.
- Separa a saída dos comandos nativos dos diagnósticos para melhorar o processamento de arquivos e preservar os erros detalhados das tarefas.
- Melhora as mensagens de erro de atualização e a detecção da arquitetura em Macs Intel e Apple silicon.

## Problema conhecido

- Uma falha SQLite SIGBUS observada em uma validação anterior ainda não foi reproduzida e resolvida de forma conclusiva.
