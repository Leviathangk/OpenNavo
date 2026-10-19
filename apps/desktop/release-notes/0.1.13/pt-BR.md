## Novidades

- Adiciona um firewall opcional de conexões de saída, com regras por app e destino, perguntas sobre conexões, histórico e listas de bloqueio. Ative no OpenNavo e aprove a extensão do sistema e o filtro de rede do macOS quando solicitado.
- Oferece controles e explicações do firewall nos seis idiomas. Se a extensão não conectar, o app informa o estado e se é preciso reiniciar.

## Melhorias

- Os painéis da barra de menus abrem sem trazer a janela principal para a frente e fecham de forma mais consistente.
- Melhora as mensagens sobre a conta da App Store e informa claramente quando falta o componente de download.
- Os controles de inicialização respondem durante a verificação, sem que resultados antigos revertam as alterações.
- Protege os arquivos SQLite em uso e seus aliases contra verificações de segurança, preservando os bloqueios do banco de dados.
- Amplia as páginas de segurança do site e mantém elegíveis para indexação os apps pausados apenas pela regra de atualização do catálogo.

## Limites do firewall

- Controla conexões TCP e UDP de saída. Conexões desconhecidas são permitidas quando o app está indisponível ou a pergunta expira; não é uma barreira que bloqueia tudo em caso de falha.
- Regras de domínio precisam de um nome de host fornecido pelo macOS. Sem ele, conexões podem seguir regras de IP, porta e app.

## Problema conhecido

- Uma validação anterior apresentou uma falha SQLite SIGBUS. Esta versão adiciona proteção de bloqueios e testes de regressão, mas a falha original ainda não foi reproduzida e resolvida de forma conclusiva.
