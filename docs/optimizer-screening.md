# Stage 0 — render-free optimizer screening

Budget 1500 evaluations, 20 seeds per cell, one worker per run, lower is better. Cells are medians over seeds.

Regenerate with `just opt-screen` (or `OPT_SCREEN=1 OPT_SCREEN_OUT=$PWD/docs/optimizer-screening.md go test -run TestOptimizerScreening ./cmd/piano-fit/`).

## sphere

| config             |        5d |       9d |    20d |   30d |
| ------------------ | --------: | -------: | -----: | ----: |
| baseline           |   0.00288 |   0.1896 |  4.601 | 15.93 |
| random             |     2.672 |    15.64 |     91 | 167.1 |
| halton             |     2.051 |    13.57 |  84.28 | 162.5 |
| warm-start         |  0.000414 |   0.0273 |  1.142 | 5.664 |
| iters-40           | 9.895e-07 | 0.003956 | 0.5798 | 3.431 |
| iters-120          | 9.895e-07 | 0.003956 | 0.5798 | 3.431 |
| iters-single-round | 9.895e-07 | 0.003956 | 0.5798 | 3.431 |
| stagnation-15      |   0.00288 |   0.1896 |  4.601 | 15.93 |
| dance-damp-0.95    |  0.003243 |   0.1766 |  4.782 | 17.68 |
| dance-damp-0.99    |  0.002426 |   0.1648 |  5.016 | 16.84 |
| nc-ratio-1         |  0.002103 |   0.1509 |  5.069 | 17.93 |
| pop-20             |   0.02803 |   0.4473 |  6.667 | 24.32 |
| pop-40             |    0.1208 |    1.699 |  26.28 | 61.96 |
| warm+iters-120     | 1.327e-06 |  0.00285 | 0.6576 | 3.323 |
| variant-ma         |  0.002681 |   0.1581 |  5.359 | 16.96 |
| variant-desma      |   0.00288 |   0.1896 |  4.601 | 15.93 |
| variant-olce       |  0.001789 |  0.06959 |  2.579 | 13.88 |
| variant-eobbma     |   0.00476 |    0.298 |  15.48 | 35.37 |
| variant-gsasma     |  0.006397 |   0.2337 |  5.439 | 21.89 |
| variant-mpma       |  0.003288 |    0.165 |  4.739 | 16.76 |
| variant-aoblmoa    |     1.041 |    10.99 |  59.65 | 117.1 |

## rastrigin

| config             |    5d |    9d |   20d |   30d |
| ------------------ | ----: | ----: | ----: | ----: |
| baseline           | 4.597 |  23.1 | 122.9 | 214.2 |
| random             | 24.25 | 75.99 | 256.7 | 437.2 |
| halton             | 27.31 | 63.47 | 278.2 | 451.3 |
| warm-start         |  2.05 | 11.88 | 89.68 | 190.3 |
| iters-40           |   2.9 | 16.62 | 74.03 | 148.1 |
| iters-120          |   2.9 | 16.62 | 74.03 | 148.1 |
| iters-single-round |   2.9 | 16.62 | 74.03 | 148.1 |
| stagnation-15      | 4.597 |  23.1 | 122.9 | 214.2 |
| dance-damp-0.95    | 4.205 | 20.88 | 121.2 |   229 |
| dance-damp-0.99    | 4.336 | 23.25 | 120.6 |   228 |
| nc-ratio-1         | 3.635 | 22.09 | 119.9 |   231 |
| pop-20             | 6.903 | 32.65 | 146.1 | 268.2 |
| pop-40             | 11.98 |  47.9 | 179.9 | 312.2 |
| warm+iters-120     | 3.005 | 15.59 | 67.36 | 163.6 |
| variant-ma         |  3.48 | 21.73 | 121.5 | 243.9 |
| variant-desma      | 4.597 |  23.1 | 122.9 | 214.2 |
| variant-olce       | 3.697 | 19.71 | 112.8 | 236.6 |
| variant-eobbma     | 4.909 | 21.54 | 116.5 | 228.8 |
| variant-gsasma     | 5.555 | 25.82 | 120.3 | 237.8 |
| variant-mpma       |  5.58 | 26.58 | 134.9 | 252.8 |
| variant-aoblmoa    | 20.79 | 69.39 | 220.2 | 381.9 |

## rosenbrock

| config             |     5d |    9d |   20d |       30d |
| ------------------ | -----: | ----: | ----: | --------: |
| baseline           | 0.6644 | 19.53 | 206.2 |     553.2 |
| random             |  20.92 | 421.4 |  4161 | 1.225e+04 |
| halton             |  17.74 | 166.6 |  2834 |      6598 |
| warm-start         | 0.9821 | 16.72 | 111.5 |     223.7 |
| iters-40           |  0.523 |  16.3 | 110.8 |     251.8 |
| iters-120          |  0.523 |  16.3 | 110.8 |     251.8 |
| iters-single-round |  0.523 |  16.3 | 110.8 |     251.8 |
| stagnation-15      | 0.6644 | 19.53 | 206.2 |     553.2 |
| dance-damp-0.95    | 0.7284 | 19.64 | 226.2 |     615.6 |
| dance-damp-0.99    | 0.7421 | 19.97 | 213.8 |     661.2 |
| nc-ratio-1         | 0.5107 | 21.43 | 236.5 |     655.3 |
| pop-20             |  1.522 | 27.88 | 397.9 |      1085 |
| pop-40             |  4.463 | 85.08 |  1133 |      3706 |
| warm+iters-120     | 0.3584 | 15.82 | 108.5 |     181.5 |
| variant-ma         | 0.3784 |  20.3 | 251.8 |     612.2 |
| variant-desma      | 0.6644 | 19.53 | 206.2 |     553.2 |
| variant-olce       |  1.074 | 18.17 |   181 |       533 |
| variant-eobbma     |  1.623 | 41.73 | 492.2 |      1099 |
| variant-gsasma     |  1.337 | 21.01 |   189 |     514.9 |
| variant-mpma       | 0.4295 | 21.98 | 211.2 |     631.4 |
| variant-aoblmoa    |  14.56 |   136 |  1774 |      4309 |

## Measured evaluations per mayfly iteration

The round-length derivation assumes 20. Anything above that means rounds are truncated mid-search rather than completing.

| config             | median evals/iteration |
| ------------------ | ---------------------: |
| baseline           |                   47.7 |
| warm-start         |                   47.7 |
| iters-40           |                   46.5 |
| iters-120          |                   46.2 |
| iters-single-round |                   46.0 |
| stagnation-15      |                   47.7 |
| dance-damp-0.95    |                   47.7 |
| dance-damp-0.99    |                   47.7 |
| nc-ratio-1         |                   37.9 |
| pop-20             |                   92.7 |
| pop-40             |                  193.7 |
| warm+iters-120     |                   46.2 |
| variant-ma         |                   42.7 |
| variant-desma      |                   47.7 |
| variant-olce       |                   52.7 |
| variant-eobbma     |                   43.6 |
| variant-gsasma     |                   44.8 |
| variant-mpma       |                   42.7 |
| variant-aoblmoa    |                   54.8 |
