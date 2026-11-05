## Novidades

- A limpeza agora pode remover caches regeneráveis, registros e downloads abandonados de ferramentas de programação com IA, incluindo Codex, Claude Code, Cursor, Antigravity, Continue e opencode. Sessões, memórias e credenciais permanecem.
- Em um projeto que você ainda usa, os arquivos de build incremental podem ser removidos sozinhos. Isso só deixa o próximo build mais lento. Reconstruir o projeto inteiro continua sendo outra opção e começa desmarcada.
- Os armazenamentos do histórico de IA continuam somente leitura. Arquivos de transcrição com mais de 30 dias podem ir para o Lixo um de cada vez. A revisão mostra quantas sessões cabem em 30, 90, 180 e 365 dias, além das datas mais antiga e mais recente.
- Os pesos de modelo baixados do Ollama, LM Studio, Hugging Face, Jan e GPT4All podem ir para o Lixo. Conversas, conjuntos de dados e índices de documentos locais permanecem. Se essa ferramenta estiver em execução, o armazenamento dela fica no lugar. O MCP local não pode selecionar esses itens.
- Worktrees do Git e clones duplicados aparecem com o espaço que ocupam. Um worktree vinculado só é removido quando o Git aceita, então trabalho não commitado é recusado; o branch e o repositório principal ficam. Um clone duplicado vai para o Lixo somente quando está limpo, totalmente enviado e sem stash. Um worktree quebrado é mostrado e não pode ser removido aqui. Linhas além do limite da lista são contadas, não exibidas.

## Melhorias

- Um projeto sem edições no código-fonte por 30 dias pode oferecer o resultado do build para limpeza. Um projeto com edições recentes continua ativo, então só o build incremental, menor, é oferecido.
- Arquivos dos últimos 7 dias continuam somente leitura. Um alvo de 7 a 30 dias é oferecido desmarcado. Só um alvo que a verificação de atividade considerou parado começa marcado. Uma pasta e os arquivos dentro dela não são contados duas vezes.

## Correções

- Cada cache do Cursor aparece com a própria evidência.
- A limpeza de modelos locais lista só arquivos de modelo, e só quando esse diretório existe.
- Remover um worktree confere de novo se a pasta ainda pertence àquele repositório.
