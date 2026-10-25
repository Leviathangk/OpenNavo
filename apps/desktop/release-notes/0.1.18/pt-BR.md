## Melhorias

- Evita que leituras da biblioteca, preferências de atualização e atualizações da bandeja bloqueiem a janela enquanto aguardam acesso ao disco ou bloqueios.
- Verifica novamente candidatos de atualização nativa desatualizados após reiniciar ou remontar, sem pular verificações agendadas e atualizações automáticas.
- Impede que respostas antigas sobrescrevam estados mais recentes de tarefas, atualizações e firewall; preserva alterações em regras e listas de bloqueio durante o carregamento.
- Melhora os textos em chinês tradicional e reduz consultas repetidas às fontes de atualização ao ler o inventário de aplicativos.

## Problema conhecido

- Uma falha SQLite SIGBUS observada em uma validação anterior ainda não foi reproduzida e resolvida de forma conclusiva.
