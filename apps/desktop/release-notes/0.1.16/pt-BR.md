## Melhorias

- Reduz o trabalho recorrente de CPU, sistema de arquivos e banco de dados nas verificações de segurança em segundo plano, na atividade do firewall e no monitoramento das ventoinhas, mantendo os sinais de conexão a cada cinco segundos.
- Repete verificações de inicialização rejeitadas em varreduras posteriores, detecta substituições atômicas de downloads e mantém o intervalo da consulta alternativa quando os eventos do sistema de arquivos são desconectados.
- Melhora a amostragem do monitor de recursos, as leituras de processos e a atualização dos gráficos.
- Melhora o tratamento de assinaturas de pacotes, caminhos de instalação e solicitações de administrador nas instalações da App Store.
- Coleta automaticamente dados anônimos de uso diário, inclusive em segundo plano. Os relatórios contêm um identificador aleatório de instalação, horário, plataforma, versão do aplicativo e idioma; o botão anterior em Configurações foi removido.

## Problema conhecido

- Uma falha SQLite SIGBUS observada durante uma validação anterior ainda não foi reproduzida e resolvida de forma conclusiva.
