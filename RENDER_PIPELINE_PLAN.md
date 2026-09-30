# Plano de melhoria do render de apresentações

## Objetivo

Reduzir o tempo de exportação mantendo os quadros, a duração, os timestamps,
o áudio e a aparência das apresentações. A prioridade é eliminar transferências
de quadros entre FFmpeg, Go e Chromium. Aceleração por GPU vem depois de medir
quanto tempo ainda é gasto na composição e na codificação.

Este plano trata de `backstage render` e `backstage preview` para apresentações.
A gravação de cenas e o comando `produce` têm pipelines próprios.

## Diagnóstico

O caminho `frames` prepara as trilhas em FFV1, decodifica cada quadro como PNG,
envia esse PNG em base64 ao Chromium, aguarda a composição HTML, captura outro
PNG e o envia ao FFmpeg para codificação H.264. Ele é necessário
quando o HTML ou a geometria mudam de forma que o compositor direto não
consegue reproduzir. O caminho `layered` já mantém o vídeo no FFmpeg para
trechos elegíveis, usando capturas estáticas do HTML.

`encode-seconds` inclui a espera pelos quadros produzidos pelo Chromium; não é
uma medida isolada do encoder. Para localizar gargalos, usar as fases `decode`,
`transfer`, `draw`, `screenshot`, `encode-write`, `probe` e
`composite-seconds` do arquivo `.facts.json`, além do tempo total em cache frio.

## Estado implementado

1. **Mais trechos estáveis no caminho `layered`.** O planejador corta chunks
   quando a visibilidade de uma legenda muda e quando uma transição termina.
   Assim, um chunk que só começa com uma transição não obriga seu restante a
   usar screenshots. Nos exemplos, a cobertura passou de zero para 42 de 96
   quadros em `complete` e 18 de 72 em `short`. Esses números indicam cobertura,
   não um ganho de tempo garantido. Nenhum corte deixa um pedaço com menos de
   3 quadros: os encoders H.264 não aplicam o atraso de DTS dos B-frames a
   codificações mais curtas, e a concatenação de segmentos com e sem esse
   atraso gerava timestamps irregulares, com quadros duplicados e perdidos
   na decodificação.
2. **Fade de HTML estático no FFmpeg.** Quando os dois eventos declaram
   `static`, não têm trilhas em slots nem legendas visíveis durante o chunk, e
   a sondagem não encontra animação ou mídia dinâmica, o Chromium captura cada
   evento uma vez. O FFmpeg calcula os quadros do fade. Os demais fades e os
   morphs seguem o caminho adequado às suas características. A receita desses
   chunks tem uma chave de cache própria.

Uma comparação local de três pares com cache frio para um fade estático de
1 segundo a 30 fps mediu medianas de **3,360 s em `frames`** e **0,894 s em
`auto`**. O PSNR-Y mínimo foi **47,86 dB** e a média **54,30 dB**. Um preview
iniciado no meio do fade preservou os PTS e passou na comparação visual. O
fade com SVG dinâmico do exemplo `short` continua no caminho de screenshots.

3. **Workers dimensionados por quadros.** Cada worker abre um Chromium, e os
   cortes dos itens anteriores multiplicavam os chunks sem aumentar o trabalho.
   O número automático de workers passou a ser limitado por
   `ceil(quadros perdidos / max(16, 2*fps))`, e cada worker recebe uma fatia
   contígua de chunks escolhida para minimizar o maior total de quadros.
4. **Preparação em paralelo com o Chromium.** A busca no cache de segmentos
   vem primeiro; em seguida, as trilhas são preparadas enquanto os workers
   iniciam o Chromium, e a mixagem roda junto com os chunks. Só o mux final
   espera a mixagem.
5. **Mixagem reproduzível.** O tamanho dos blocos que o `amix` entrega depende
   do tempo das threads do FFmpeg, e o fim da latência do `alimiter` depende
   desse tamanho. Sob carga, os últimos milissegundos do áudio variavam entre
   renders. A mixagem agora fixa blocos de 1024 amostras antes do limitador.
6. **Encoder H.264 na GPU.** `render.encoder: "vaapi"` codifica os chunks com
   `h264_vaapi` (CQP 20) nos três caminhos (`frames`, `layered` e fade
   estático); `libx264` continua o padrão. Antes de qualquer chunk, uma prova
   codifica um quadro no tamanho do render e registra a linha do driver VAAPI,
   que entra na chave do cache de segmentos. Se a prova falhar, o render para
   sem tocar na saída existente. Contra `libx264`, o exemplo `complete` ficou
   com PSNR-Y mínimo de 46,05 dB e médio de 51,98 dB. Numa medição preliminar
   com a máquina carregada (projeto sintético de 60 s em 1080p30), a mediana
   de CPU caiu de 421,5 s para 365,9 s e a do tempo total de 92,8 s para
   86,3 s, porque o encoder deixa de disputar CPU com os Chromium.
7. **Stills decodificados uma vez.** Com `-loop 1`, o FFmpeg decodificava os
   PNGs de fundo, máscara e topo a cada quadro de saída. A composição `layered`
   e o fade estático agora leem cada still uma vez e o repetem com o filtro
   `loop`. Os timestamps são inteiros na base `1/fps` (`settb` e `setpts=N`):
   `setpts=N/fps/TB` trunca 2,999… para 2 a 10 fps, carimba dois quadros
   iguais e desalinha fade e overlay. Num chunk
   de 60 quadros em 1080p com duas trilhas, a saída ficou idêntica bit a bit
   e a mediana de CPU caiu de 9,6 s para 3,7 s (tempo total de 1,75 s para
   1,18 s).

Avaliados e descartados: stills PAM em vez de PNG (mais CPU no FFmpeg), fatias
menores por worker (mais inicializações de Chromium sem ganho), Chromium com
GPU (screenshots mais lentos) e trilhas intermediárias em H.264 sem perdas no
lugar de FFV1 (mesmo tempo total, só 2,8% menos CPU, e não preserva RGB exato).

Medição final: projeto sintético de 60 s em 1080p30, cache frio, 3 rodadas
com ordem alternada. A máquina estava sob carga externa (load average de 20 a
31, de outros processos), então o tempo total oscila; o CPU é o indicador mais
confiável.

| modo | mediana tempo total (s) | mediana CPU (s) |
|---|---|---|
| HEAD | 40,6 | 251,7 |
| novo, `libx264` | 38,3 | 193,2 (−23%) |
| novo, `vaapi` | 38,1 | 173,7 (−31%) |

PSNR-Y contra o HEAD: `libx264` com mínimo de 48,03 dB e médio de 93,12 dB;
`vaapi` com mínimo de 47,74 dB e médio de 54,30 dB.

## Marcos avaliados e não implementados

### Composição híbrida quando o HTML muda

A ideia era o Chromium fornecer só as camadas HTML e a geometria, e o FFmpeg
posicionar as trilhas, eliminando vídeo → PNG → base64 → Chromium nos trechos
dinâmicos. No projeto sintético de 60 s, só 150 dos 1800 quadros passaram pelo
caminho de screenshots, e os quadros com vídeo e HTML mudando ao mesmo tempo
(as transições `morph`) são cerca de 30 deles. O restante é HTML animado sem
vídeo, que a composição híbrida não alcança. A transferência de quadros de
vídeo custou 9 s de CPU nos 150 quadros, contra cerca de 400 s do render
inteiro; o ganho possível, perto de 2 s, não justifica um segundo compositor.

### Composição na GPU

Um grafo equivalente com `overlay_vulkan` (Radeon 840M, FFmpeg 9.0.1) ficou
dentro do limite de qualidade (PSNR-Y mínimo de 46,27 dB contra a composição
na CPU), mas levou 11,8 s por chunk de 60 quadros em 1080p contra 0,93 s na
CPU. Enviar as trilhas e as camadas à GPU e trazer o resultado de volta ao
encoder custa mais do que a composição que sai da CPU.

## Critérios para aceitar cada mudança

- Comparar com `frames` em cache frio: **PSNR-Y de pelo menos 40 dB em cada
  quadro e 45 dB em média**, além de contagem de quadros, PTS, duração e áudio
  alinhados.
- Cobrir render completo, preview que começa no meio de um chunk, legendas,
  transições, fim de trilha, cache quente, cancelamento e erro do FFmpeg.
- Medir pelo menos três pares alternando a ordem dos modos na mesma máquina.
  Registrar a mediana do tempo total e dos chunks afetados; não aceitar uma
  rota mais complexa com base apenas em uma execução favorável.
- Preservar um único caminho de fallback para conteúdo não elegível. Remover
  lógica substituída, evitar duplicação entre o runtime e o compositor e
  atualizar apenas a documentação do comportamento vigente.

Os pontos de entrada são `internal/presentation/chunks.go` para seleção e
execução de chunks, `workers.go` para seus limites, `layers.go` para composição,
`still_fade.go` para o fade estático e `segments.go` para chaves de cache.
