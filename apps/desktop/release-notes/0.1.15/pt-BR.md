## Melhorias

- Adiciona estatísticas de uso anônimas e opcionais das instalações do Mac em execução, inclusive em segundo plano. Ficam desativadas por padrão e podem ser ativadas nas Configurações; os relatórios contêm apenas um identificador aleatório, horário, plataforma, versão do aplicativo e idioma. Desativá-las apaga o identificador local e os relatórios pendentes.
- Verifica atualizações do OpenNavo a cada hora quando as verificações automáticas estão ativadas, sem interrompê-las após a primeira verificação bem-sucedida do dia.
- Impede que a atualização das informações do Homebrew na inicialização suprima as verificações agendadas das fontes de atualização selecionadas.

## Problema conhecido

- Uma falha SQLite SIGBUS observada durante uma validação anterior ainda não foi reproduzida nem resolvida de forma conclusiva.
