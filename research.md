# Realtime-ultrarealistische physikalische Klavier-Synthese

## Executive Summary

Die heute praktikabelste „ultra-realistische“ Echtzeit-Architektur (CPU-tauglich, polyphon, niedrige Latenz) ist **hybrid**: (a) **Saiten als modale Resonatorbänke** (Transversal + wichtige Longitudinalanteile), (b) **nichtlineare Hammer–Saiten-Kontaktmodelle** (Power-Law / Hunt–Crossley bzw. hysteretisch) mit **stabiler Zeitdiskretisierung**, (c) **Korpus/Soundboard als lineares System** (reduziertes Modalmuster oder _FIR/FFT-Faltung_), (d) **Abstrahlung** über eine kleine Strahlungs-/Mikrofon-Übertragungsfunktion (Modal-Radiation, Rayleigh-basierte Approximation oder Mess‑IR) – alles streng in **Echtzeit-Blockverarbeitung**. Diese Stoßrichtung wird durch die Literatur zu Realtime-Piano-Modellen gestützt: Bank/Zambon/Fontana zeigen, dass ein modales Komplettmodell mit aufwändiger Soundboard-Faltung bei voller Polyphonie auf Consumer-CPU machbar ist (10 000 Resonatoren + vier 20 000‑Tap‑Faltungen bei ca. 30 % Last auf Core‑2‑Duo). citeturn20view0turn7view0

Für maximale physikalische Nähe (bis hin zu Luftfeld/3D) existieren gekoppeltere, energieerhaltende PDE/FEM/FDTD‑Gesamtmodelle (Saiten + orthotropes, geripptes Soundboard + Akustikfeld), die allerdings **hohe Rechenleistung/Parallelisierung** benötigen; exemplarisch modelliert Chabassier/Chaigne/Joly die große Klavierkette inklusive 3D-Akustikfeld (PML, energieerhaltende Schemen, Lagrange‑Multiplikatoren/Schur‑Komplement-Entkopplung) und betont die Notwendigkeit von Hochleistungsrechnen für das 3D‑Feld. citeturn16view2

Ein zentrales „Realismus‑Hebelgesetz“ aus der Klavierakustik: **Das Soundboard ist im Spielbetrieb weitgehend linear** (nichtlinearer Anteil ca. −40 dB unter dem linearen Anteil bei ff), sodass **lineare Reduktionsmodelle** (Modalreduktion, Mobilitätssynthese, IR‑Filter) sehr viel Realismus pro CPU liefern. citeturn8view0

## Literatur und Stand der Technik

**Priorisierte Kernliteratur (≈1990–heute, Fokus Realtime + Realismus):**

| Priorität | Schwerpunkt                                                                   | Primärquelle (Auswahl)                                                                                  | Warum „tragend“                                                                                              |
| --------- | ----------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------ |
| Sehr hoch | Hammered stiff string als PDE + FDTD-Grundlage                                | Chaigne & Askenfelt, _Numerical simulations of piano strings I_ (1994) citeturn17view0turn12search7 | Klassische Referenzgleichungen + FDTD‑Schema inkl. Stabilitäts-/Sampling-Überlegungen                        |
| Sehr hoch | PDE ↔ FDTD ↔ Digital Waveguides, Parameterkalibrierung                        | Bensa/Bilbao/Kronland‑Martinet/Smith, JASA 2003 citeturn9view0turn7view1                            | Verknüpft physikalische PDE‑Parameter mit DWG‑Filtern; zeigt Genauigkeits-/Dispersionsthemen                 |
| Sehr hoch | Realtime‑Piano via Modal-Synthese (Strings + Soundboard-Faltung)              | Bank/Zambon/Fontana, IEEE TASLP 2010 citeturn7view0turn20view0                                      | Konkrete Realtime‑Komplexität, Alias‑Kontrolle, Multi‑Rate‑Architektur                                       |
| Sehr hoch | Globales physikalisches Modell (Saiten + Soundboard + Luft), energieerhaltend | Chabassier/Chaigne/Joly, JASA 2013 citeturn16view2                                                   | „Goldstandard“ für gekoppelte Physik (inkl. Lagrange‑Multiplikatoren/Schur) – als Referenz für High‑Fidelity |
| Hoch      | Soundboard: Linearität, Moden, Dämpfung bis ~3 kHz                            | Ege/Boutillon/Rébillat, JSV 2013 citeturn8view0                                                      | Begründet lineare Modellierung + zeigt Übergang in rib‑lokalisierte Regime                                   |
| Hoch      | Soundboard-Mobilität/Messdaten → reduzierte Modelle                           | Ege & Boutillon (Mobilitätssynthese, bis ~2.5 kHz) citeturn21view2turn21view3                       | Praktische Reduktion: Mobilität aus wenigen Größen (Modal­dichte, Verlustfaktor, Masse)                      |
| Hoch      | Kontakt-/Stoßnumerik: energie-stabile Schemen                                 | Chatziioannou & van Walstijn, JSV 2015 citeturn7view3turn4search0                                   | Systematische, energieerhaltende Diskretisierungen (inkl. Lagrange‑Multiplikatoren / Nichtpenetration)       |
| Hoch      | Kollisionen als Potential/Power‑Law + Energiebilanzen                         | Bilbao/Torin/Chatziioannou (2015, „Collisions…“) citeturn3search0                                    | Zeigt, wie Stabilität über Energieerhaltung/-dissipation robust wird (wichtig für Hammerkontakt)             |
| Hoch      | Unisono-/Mehrfachsaiten-Kopplung (Beats, double decay)                        | Aramaki et al. (1999, gekoppelte Waveguides) citeturn8view3turn6search19                            | Modelliert perceptually essentielle Unisono-Effekte mit effizienter Kopplung                                 |
| Ergänzend | Soundboard‑FE/Moden + Rib‑Geometrie                                           | Chaigne/Cotte/Viggiano, JASA 2013 citeturn8view1turn25search11                                      | Liefert konkrete Rib‑Abstände/FE‑Modellierung und Zusammenhang zu Lokalisation/Abstrahlung                   |

**Deutsche/nah verwandte (DACH) Ankerpunkte:**  
Mess-/Übertragungsanalysen am realen Klavier (Multichannel‑Messung) sind u.a. in DAGA‑Proceedings dokumentiert. citeturn5search23 Parallel gibt es kompaktere „physics‑based“ Modellskizzen (z.B. DAGA 2016). citeturn12search29

## Physikalisches Referenzmodell eines Konzertflügels

### Saite als gedämpfte, steife Saite (Transversal)

Ein in der Klavierliteratur sehr verbreiteter Ansatz ist eine **steife, gedämpfte Saite** (Euler‑Bernoulli‑Steifigkeit) mit frequenzabhängiger Dämpfung, z.B. in der Bauform, wie sie Chaigne/Askenfelt und Bensa et al. verwenden. citeturn17view0turn7view1

Eine typische 1D‑PDE (schematisch, für Auslenkung \(y(x,t)\)) lautet:
\[
\rho A\, y*{tt}
= T\, y*{xx} - E I\, y\_{xxxx}

- 2\rho A\,\sigma*0\, y_t + 2\rho A\,\sigma_1\, y*{txx}

* f_h(x,t),
  \]
  mit Spannung \(T\), Biegesteifigkeit \(EI\), linearer Dämpfung \(\sigma_0\) und „diffusionsartiger“ Dämpfung \(\sigma_1\) (typisch: stärker für hohe Wellenzahlen). Die konkrete Parametrisierung als \(c, k, b_1, b_2\) (Wellen­geschwindigkeit, Dispersions-/Steifigkeitsparameter, Verlustterme) ist in Bensa et al. explizit tabelliert und wird zur DWG‑Kalibrierung genutzt. citeturn9view0turn7view1

**Modale Darstellung (Realtime-freundlich):**  
Für viele Echtzeitsysteme wird die Saite als Summe von Moden dargestellt:
\[
y(x,t)=\sum\_{n=1}^{N} q_n(t)\,\phi_n(x),
\]
wobei jede Mode \(q_n(t)\) als gedämpfter 2.‑Ordnung‑Resonator mit Eigenfrequenz \(f_n\) und Abklingzeit/Q umgesetzt wird. Bank et al. implementieren jede Mode als diskretes 2‑Pol‑Filter (impulsinvariant abgeleitet) und diskutieren Alias‑Probleme bei nichtlinearen Kopplungen explizit. citeturn20view3turn7view0

### Longitudinalanteile und nichtlineare Saitenspannung

Für wirklich „klaviertypische“ Transienten (z.B. helle Attack‑Anteile, „Phantom“-Komponenten) spielen neben Transversalmoden auch **Longitudinaleffekte** und Nichtlinearitäten (Spannungsmodulation) eine Rolle; Bank et al. geben eine konkrete, modal implementierbare Prozedur zur Berechnung der longitudinalen Brückenkraft als Funktion der Transversalmoden und zeigen, dass in der Praxis nur die ersten \(K\approx 2\dots10\) longitudinalen Resonanzen dynamisch behandelt werden müssen. citeturn20view2turn7view0

### Hammer–Saite Kontakt: Power‑Law, Hysterese, Hunt–Crossley

Der Hammerfilz zeigt eine stark nichtlineare Kraft‑Kompressions‑Charakteristik. Ein gebräuchlicher Kern ist ein **Power‑Law**:
\[
F = K\,\delta^{p}, \qquad \delta = y_h - y_s(x_h,t),\quad \delta>0,
\]
wobei \(p\) hörbar die „Helligkeit“ (Energie in hohen Teiltönen) beeinflusst. Boutillon berichtet exponentielle Fits mit \(\alpha\) (entspricht \(p\)) z.B. um 2.1, 3.5 oder 5 (je nach Hammer/Zone), und koppelt das mit hysteretischem Verhalten. citeturn19search3  
In wave-digital/passiven Formulierungen wird ein ähnliches Power‑Law (historisch u.a. Ghosh) als Standardannahme genannt, typischerweise mit Exponenten im Bereich \(2\dots3\) (als „typisch“ in dieser Modellfamilie). citeturn23view3

**Dissipatives Kontaktmodell (Hunt–Crossley‑Familie):**  
Zur Abbildung von Kontaktverlusten wird oft eine Hunt–Crossley‑artige Dämpfung genutzt (Kraft abhängig von Eindringtiefe _und_ Relativgeschwindigkeit). Chatziioannou & van Walstijn diskutieren explizit Hunt–Crossley als Erweiterung eines Power‑Law‑Kontakts und verweisen auf das Original (Hunt & Crossley 1975). citeturn10view3turn3search2

### Soundboard/Resonanzboden als lineares, orthotropes, geripptes Plattensystem

Mess- und Modellarbeiten zeigen zwei für Realtime entscheidende Eigenschaften:

Erstens ist der Resonanzboden im üblichen Spielbetrieb **nahezu linear** (Nichtlinearanteil etwa −40 dB unter linear bei ff), was lineare Reduktionsmodelle rechtfertigt. citeturn8view0

Zweitens zeigt er **zwei Regime**: unter ca. 1 kHz verhält er sich eher wie eine (orthotrope) „homogene Platte“, darüber werden Wellen durch Rippen „eingesperrt“ und Moden lokalisieren in Inter‑Rippen‑Bereichen. citeturn8view0turn25search2

Ein physikalisch „voller“ Ansatz (als Referenz) ist eine dissipative **orthotrope Reissner–Mindlin‑Platte** mit lokalen Heterogenitäten (Rippen/Stege), wie sie im globalen Modell von Chabassier/Chaigne/Joly genutzt wird. citeturn16view2

### Brücken-Kopplung und Endbedingungen

Die Saite sieht an der Brücke keinen perfekt starren Abschluss, sondern eine dynamische Last (Mobilität/Impedanz). Mobilitätsbasierte Beschreibungen formulieren die Endbedingung über
\[
Y(\omega)=\frac{V(\omega)}{F(\omega)}
\]
am Brückenpunkt und nutzen Modal­dichte/Verlustfaktor/Masse zur Synthese der Mobilität (Skudrzyk/Langley‑basierte Ansätze). citeturn21view2turn21view3

In zeitdiskreten Kopplungsmodellen wird häufig eine **Kontaktsteifigkeit** \(k_c\) verwendet:
\[
F_b = k_c\,(y_s - y_b),
\]
wobei eine Größenordnung explizit berichtet wird: für eine Kontaktlänge \(L_c\approx 0{,}01\,\mathrm{m}\) wird \(k_c \approx 4{,}8\times10^{6}\,\mathrm{N/m}\) abgeschätzt (aus Hertz/Popov‑Ableitungen). citeturn9view3turn10view0

### Unisono-/Mehrfachsaiten und Verstimmung

Ein wesentlicher Piano‑Klangbestandteil ist die Kopplung mehrerer Saiten pro Ton (2–3 Saiten) mit sehr naher Stimmung: das erzeugt **Beats** und **Double‑Decay**. Aramaki et al. zeigen, dass gekoppelte Waveguides diese Effekte reproduzieren können und betonen explizit Beats/double decay als perceptually relevant. citeturn8view3turn6search19  
Auch Bensa et al. diskutieren explizit Inter‑String‑Kopplungen als nächsten Schritt nach dem Einzelsaitenmodell. citeturn6search0turn12search18

### Abstrahlung: von Rayleigh-Integral bis modalem Strahlungsfilter

Für reale Klavier‑„Air/Room“‑Nähe ist die Umsetzung des **akustischen Transfers** kritisch. Ein physikalischer Referenzweg ist das Rayleigh‑Integral für (nahezu) baffled, flache Strahlerflächen (Druck aus der Oberflächengeschwindigkeit durch Flächenintegral mit Green‑Funktion‑Kernel). citeturn25search28turn25search32  
Ege/Boutillon zeigen zudem, dass die „Acoustical radiation regime“ (inkl. Coincidence‑Phänomen) beim gerippten Piano‑Soundboard deutlich anders ist als bei einer homogenen Platte; das motiviert modellbasierte Strahlungsfaktoren statt „einfacher Platte“. citeturn25search2turn21view1

## Diskretisierung, Kopplung und Kontakt in Echtzeit

### Methodenvergleich und zentrale Trade-offs

| Methode                                                       | Realismus-Potenzial                                          | CPU/GPU-Kosten             | Latenz                         | Implementierungsaufwand                            | Erweiterbarkeit                        |
| ------------------------------------------------------------- | ------------------------------------------------------------ | -------------------------- | ------------------------------ | -------------------------------------------------- | -------------------------------------- |
| **Modal (Resonatorbank)**                                     | Sehr hoch für Saiten; Soundboard sehr gut, solange linear    | Niedrig–mittel (O(#Moden)) | Sehr niedrig (sample‑basiert)  | Mittel (Parameter + Alias/Nonlinearitäten)         | Sehr hoch (leicht koppelbar, polyphon) |
| **Digital Waveguide (DWG)**                                   | Sehr hoch für lineare Saiten; Dispersion/Loss sehr effizient | Sehr niedrig               | Sehr niedrig                   | Mittel (Dispersion-/Loss‑Filterdesign, Kopplungen) | Hoch (Netzwerke, Multi‑Saiten)         |
| **FDTD/FEM (explizit)**                                       | Sehr hoch, inkl. Geometrie/Komplexität                       | Hoch (DOF×Zeitschritte)    | Niedrig, aber dt‑Restriktionen | Hoch (Stabilität, Dispersion, Randbedingungen)     | Sehr hoch (geometrisch flexibel)       |
| **Hybrid (Modal/DWG + Kontakt + lineares Soundboard‑Filter)** | In der Praxis „best bang‑for‑buck“                           | Niedrig–mittel             | Sehr niedrig                   | Mittel–hoch (Kopplung/Parameter)                   | Sehr hoch                              |
| **Monolithisch gekoppelt (Saiten+Platte+Luft)**               | Referenzniveau                                               | Sehr hoch (typisch HPC)    | Schwer realtime                | Sehr hoch                                          | Maximal                                |

**Begründete Kernaussagen aus Primärquellen:**

Digital Waveguides sind für viele piano‑Synthesen attraktiv, weil sie oft auf eine Delayline + IIR‑Loop hinauslaufen; viele Synthese‑Piano‑Modelle setzen darauf. citeturn7view0  
Bank et al. argumentieren jedoch, dass präzises Modellieren nichtlinearer Saitenschwingungen mit DWG in effizienter Form problematisch werden kann (Nichtorthogonalität der Moden/Formen durch „lumped“ Dispersionsfilter) und motivieren daher die modale Wahl. citeturn7view0

Bensa et al. zeigen in direkten Vergleichen, dass FD‑Modelle bei hohen Frequenzen u.a. durch numerische Dispersion und „künstlichen Propagations‑Gain“ abweichen können; der DWG‑Ansatz sei dort genauer bzgl. Dämpfung/Dispersion. citeturn7view1turn9view0

### Kontakt-/Stoßlöser: Stabilität ist der Engpass

Echtzeit‑„Ultrarealismus“ scheitert oft nicht an der linearen Saite, sondern an der **Kontakt‑Numerik** (Hammer, Dämpfer, Saiten‑Anschlagstellen).

**Penalty/Power‑Law (einfach, aber steif):**  
Kontakt wird über eine glatte Potentialfunktion (Power‑Law) als Penalty modelliert; das ist effizient, kann aber sehr steife Kräfte erzeugen, die kleine Zeitschritte oder implizite Behandlung erzwingen. Ein energieorientierter Rahmen (Energieerhaltung/-dissipation) ist hier besonders wertvoll, weil er numerische Stabilität absichert. citeturn3search0turn3search12

**Hunt–Crossley (realistischer dissipativ):**  
Chatziioannou & van Walstijn zeigen, wie Hunt–Crossley als Form mit zusätzlicher (nichtlinearer) Kontaktdämpfung in die Formulierung passt und nennen es explizit als Erweiterung des Power‑Law‑Kontakts. citeturn10view3turn7view3

**Komplementarität / Lagrange‑Multiplikator (rigider Kontakt, keine Penetration):**  
JSV 2015 stellt die fundamentale Unterscheidung heraus, ob Interpenetration zugelassen wird oder ein nicht-penetrativer, „perfekt rigider“ Kontakt mit Nebenbedingung (und Lagrange‑Multiplikator) umgesetzt wird. citeturn7view3turn4search0  
Solche Formulierungen sind physikalisch „sauber“, führen aber oft zu impliziten Gleichungen (Newton‑Iteration) – Realtime‑tauglich nur, wenn Iterationskosten kontrolliert werden.

**Energy‑Quadratisation / explizit‑(semi)implizit ohne Newton (Realtime‑Motivation):**  
Neuere Arbeiten berichten explizit/linear‑implizite energie‑stabile Schemen (Energy Quadratisation), die iterative Solver vermeiden und dadurch für Realtime attraktiver werden – zugleich werden Artefakt-/Oszillationsfragen bei einseitigen Kontaktkräften diskutiert. citeturn4search1turn4search9

### Kopplungsstrategien an der Brücke (String↔Soundboard)

Drei praxisrelevante Klassen:

Penalty‑Kopplung über Kontaktsteifigkeit \(k_c\): sehr einfach, robust und mit klarer Größenordnung (z.B. \(4{,}8\cdot10^6\) N/m). citeturn9view3turn10view0

Lagrange‑Multiplikator/Schur‑Komplement: im globalen Klaviermodell wird „künstliche Entkopplung“ über Schur‑Komplement und Lagrange‑Multiplikatoren genutzt, sodass Variablen getrennt pro Zeitschritt aktualisiert werden können (trotz gekoppelter Physik). citeturn16view2

Monolithisch implicit: besonders für hochsteife Kopplungen/Kontakte attraktiv (große Stabilitätsreserve), aber nur mit sehr effizientem Linear-/Nichtlinear‑Solver realtime‑fähig (oder mit MOR/GPU).

## Parameteridentifikation und typische Parameterbereiche

### Konkrete Zahlen aus der Literatur als Startpunkt

**Saiten-PDE‑Parameter über die Tastatur (Beispiel C2/C4/C7):**  
Bensa et al. geben für ein physikbasiertes Saitenmodell u.a. Länge \(L\), Wellengeschwindigkeit \(c\), Steifigkeitsparameter \(k\), Dämpfungsparameter \(b_1,b_2\) und die verwendete Abtastfrequenz \(F_s\) tabelliert. Beispielwerte:  
C2: \(L\approx1{,}23\) m, \(c\approx160{,}9\) m/s, \(k\approx0{,}58\,\mathrm{m^2/s}\), \(b_1\approx0{,}25\,\mathrm{s^{-1}}\), \(b_2\approx7{,}53\times10^{-5}\,\mathrm{m^2/s}\), \(F_s=16\,000\) s\(^{-1}\);  
C7: \(L\approx0{,}10\) m, \(c\approx418{,}6\) m/s, \(k\approx1{,}24\,\mathrm{m^2/s}\), \(b_1\approx9{,}17\,\mathrm{s^{-1}}\), \(b_2\approx2{,}1\times10^{-3}\,\mathrm{m^2/s}\), \(F_s=96\,000\) s\(^{-1}\). citeturn9view0turn7view1  
Die Spannweite zeigt unmittelbar: **hohe Register treiben die numerischen Anforderungen** (kürzere Saiten, stärkere Dämpfung, höhere \(F_s\) in der Studie). citeturn9view0

**Soundboard: orthotrope Material-/Geometriedaten (Beispiel FE‑Parameter):**  
Ein gekoppeltes String‑Soundboard‑Modell listet typische orthotrope Holzparameter: z.B. \(E*1\approx17{,}1\) GPa, \(E_2\approx1{,}04\) GPa, \(E_3\approx0{,}48\) GPa, Poisson‑Zahlen \(\nu*{12}\approx0{,}37\), etc., sowie Soundboard‑Dicke ca. 7–9 mm (modellabhängig). citeturn10view0

**Brückenkontakt-Steifigkeit:**  
Wie oben: für \(L_c\approx0{,}01\) m wird \(k_c\approx4{,}8\times10^6\) N/m angegeben. citeturn9view3turn10view0

**Resonanzboden-Linearität / Modalparameter:**  
Der Nichtlinearanteil wurde experimentell erstmals quantitativ als ca. −40 dB (bei ff) bestimmt; Modalidentifikation bis 3 kHz inkl. Dämpfungen wird berichtet. citeturn8view0  
Für Realtime folgt daraus: ein linearer Modalsatz bis wenige kHz ist „low‑risk high‑reward“.

**Hammer-Exponent und Hysterese:**  
Boutillon beschreibt Fits der Form \(F=a(\Delta y)^\alpha\) mit \(\alpha\) z.B. 2.1, 3.5, 5 (hammer-/messabhängig) und nutzt hysteretische Erweiterungen. citeturn19search3

### Identifikationsmethoden, die sich für Implementierungen bewährt haben

**Messdaten → Parameterkalibrierung der Saitenmodelle:**  
Bensa et al. kalibrieren Parameter auf einem Yamaha Disklavier‑Setup und diskutieren, wie sowohl DWG‑Loop‑Filter als auch physikalische Parameter aus Messdaten geschätzt werden. citeturn7view1turn12search18

**Perzeptionsbasierte Optimierung (Hammer‑String‑Parameter):**  
Für Hammer‑/Kontaktparameter wird explizit eine Optimierung mit perzeptiv motiviertem Kriterium (Tristimulus‑Bänder) beschrieben; als Optimierer werden Gradientenverfahren + Simulated Annealing kombiniert, um lokale Minima zu umgehen. citeturn17view1turn22search21

**Mobilitätssynthese als „Black‑Box‑Soundboard“:**  
Ein stark praxisnaher Shortcut ist, die Brückenmobilität aus wenigen Größen (Modal­dichte \(n(f)\), mittlerer Verlustfaktor \(\eta(f)\), Masse \(M\)) zu synthetisieren; das vermeidet hochparametrische Detailmodelle. citeturn21view2turn21view3

### Realtime‑„Daumenregeln“ für Modellgrößen (mit Literaturankern)

In Bank et al. ist eine konkrete Größenordnung dokumentiert: **10 000 second‑order Resonatoren** plus **vier 20 000‑Tap‑Faltungen** laufen bei voller Polyphonie auf einem damaligen Laptop mit ca. 30 % CPU‑Last; auch ist quantifiziert, dass vier 20 000‑Tap‑Faltungen ca. ein Viertel der Rechenlast von String+Hammer (bei voller Polyphonie) benötigen. citeturn20view0  
Diese Zahlen sind historisch (2010‑CPU), aber sie liefern robuste **Komplexitäts‑Skalen** für heutige Systeme.

## Empfohlene Realtime-Architektur mit Mathematik und Update-Skizzen

### Empfohlenes Systemdesign (Hybrid: modale Saiten + nichtlinearer Hammer + lineares Soundboard + Strahlungsfilter)

```mermaid
flowchart LR
  MIDI[MIDI/Key Action] -->|v_h0, key state| HAM[Hammer-Modell<br/>nichtlinearer Kontakt]
  HAM -->|F_h(t)| STR[String-Kurs<br/>N Moden pro Saite<br/>inkl. Unisono]
  STR -->|F_bridge(t), v_bridge(t)| BR[Brücke/Kopplung<br/>k_c oder Mobilität Y(ω)]
  BR --> SB[Soundboard<br/>a) reduzierte Moden<br/>b) FIR/FFT-Faltung]
  SB --> RAD[Abstrahlung/Output<br/>a) Modal-Radiation<br/>b) Rayleigh-Approx<br/>c) IR+Room]
  RAD --> OUT[Audio Out (Stereo/Spatial)]
```

**Wesentliche Gleichungen (modaler Kern, pro Saite):**

Kontaktkompression am Anschlagpunkt \(x*h\):
\[
\delta(t) = y_h(t) - y_s(x_h,t),\quad y_s(x_h,t)=\sum*{n=1}^{N} q_n(t)\phi_n(x_h).
\]

Dissipativer Kontakt (schematisch Hunt–Crossley‑Familie):
\[
F_h(t)=
\begin{cases}
K\,\delta(t)^p\bigl(1 + \gamma\,\dot{\delta}(t)\bigr), & \delta>0,\\
0, & \delta\le 0,
\end{cases}
\]
wobei die Literatur Hunt–Crossley explizit als Kontaktmodellfamilie für stoßdissipative Erweiterungen anführt. citeturn10view3turn3search2

Modal-Update (jede Mode als 2‑Pol‑Resonator):
\[
q*n[n] = a*{1,n}q*n[n-1] + a*{2,n}q_n[n-2] + b_n\,u_n[n-1],
\]
mit \(u_n\) als modaler Eingang (aus \(F_h\) über Formfunktion/Verteilung). Bank et al. geben die diskrete Pole-/Koeffizientenform explizit her (impulsinvariant, Pol \(p_k\), Dämpfung über Pole‑Radius), inklusive Begründung über \(f_s\). citeturn20view3turn7view0

Brückenkraft als Summe der Saitenbeiträge (+ optional Longitudinalterm nach Bank‑Prozedur):
Bank et al. geben eine explizite, schrittweise Prozedur zur Longitudinal‑Brückenkraft; nur wenige Longitudinalmoden \(K\) werden dynamisch korrigiert (typisch 2–10). citeturn20view2turn7view0

Soundboard als Filterblock:
_Option A (sehr robust):_ FIR/FFT‑Faltung \(p[n] = (h*{sb} \* F*{bridge})[n]\). Bank et al. diskutieren Soundboard‑Faltung und Parallel‑2.‑Ordnung‑Filter als Möglichkeiten. citeturn7view0turn20view0  
_Option B (modal reduziert):_ \( \mathbf{q}_{sb}'' + 2\mathbf{\Xi}\mathbf{\Omega}\mathbf{q}_{sb}' + \mathbf{\Omega}^2\mathbf{q}_{sb} = \mathbf{B}F_{bridge}\), Ausgabe über \(p\approx\mathbf{C}\mathbf{q}\_{sb}'\).

**Multi‑Rate‑Implementierung (praxisbewährt):**  
Bank et al. trennen Audio‑Rate (Samplingrate \(f_s\)) von MIDI‑Rate und einer langsamen Kalibrationsrate (typisch 20–50 Hz) für Parameterupdates/GUI. citeturn9view2turn7view0  
Das ist für „ultra-realistisch“ wichtig, weil Parameteridentifikation/Steuerung (z.B. Pedal‑Noise, Timbre‑Regler, Temperierung) nicht die Audio‑Thread‑Deterministik zerstören darf.

### Repräsentatives (synthetisches) Spektrogramm als Zielbild

Das folgende Spektrogramm illustriert qualitativ zwei in Pianoklängen typische Effekte: **Inharmonizität (steife Saite)** und **Unisono‑Detuning** (Beats/Schwebungen), wie sie in Unisono‑Modellen als perceptually relevant hervorgehoben werden. citeturn6search19turn8view3

![Synthetisches Spektrogramm](sandbox:/mnt/data/piano_synth_spectrogram.png)

### Alternative „höchste Treue“: monolithisches FDTD+Platte(+Akustik) und Wege zu Realtime

**Referenzschema:**  
Ein globales Zeitdomänenmodell kann Saite (inkl. geometrischer Nichtlinearität), Soundboard (orthotrop, gerippt), Kopplungen (Hammer‑String, String‑Soundboard, Soundboard‑Air) gemeinsam lösen. Chabassier/Chaigne/Joly berichten genau dieses Zielbild, nutzen energieerhaltende Schemata und koppeln über Lagrange‑Multiplikatoren; für das Akustikfeld werden PML‑Randbedingungen verwendet. citeturn16view2

**Realtime‑Machbarkeitshebel:**

- **Model Order Reduction (MOR)**: Soundboard als reduzierte Modenbasis (oder Zustandsraumreduktion) statt voller 2D‑FDTD/FEM; das passt zur experimentellen Feststellung der Linearität und zum Regimewechsel (unter ~1 kHz „plattig“, darüber rib‑geführt). citeturn8view0turn25search2
- **GPU/Parallelisierung**: Für großvolumige FDTD‑Probleme (klassisch: 3D Wave‑Equation) ist GPU‑Beschleunigung ein etablierter Weg; entsprechende CUDA‑Arbeiten im akustischen Kontext sind dokumentiert. citeturn1search10turn1search14
- **Lokales Sub‑Stepping**: Kontakt/Steifigkeit (Hammer) mit kleinerem Schritt (oder implizit) und lineare Teile mit größerem Schritt (Partitioned Time Stepping) – allerdings muss die Energie‑/Passivitätsbilanz kontrolliert bleiben (sonst „Zischen/Explosion“). Die Literatur zu energie‑stabilen Kontaktverfahren motiviert genau diese Denkweise. citeturn4search1turn7view3turn3search0
- **Abstrahlung vereinfachen**: selbst High‑Fidelity‑Strukturmodelle profitieren stark von einem linearen Strahlungs-/Room‑Block (Rayleigh‑Approx/IR‑Faltung), statt vollem 3D‑Akustikfeld pro Stimme. citeturn25search28turn22search3

## Open-Source, Daten, Validierungsexperimente und Roadmap

### Open-Source‑Codebasen (Startpunkte, praxisnah)

Open‑Source‑Piano‑Engines sind selten „wissenschaftlich perfekt“, aber sehr nützlich für Infrastruktur (MIDI, Voice‑Management, Plugin‑Packaging) und als Vergleichsbasis:

OpenPiano (Physical‑Modeling‑Piano, frühes Stadium) citeturn4search3  
FigBug/Piano (C++ VST/CLI, digital waveguide‑basiert) citeturn4search34  
Faust physmodels (Bibliothek für Physical Modeling, inkl. Strings/Membranen/Resonatoren) citeturn4search2turn4search13  
Faust‑Beispiel „piano.dsp“ (Faust‑STK‑Port) citeturn4search6  
String‑Collisions (Matlab‑Skripte zu energie‑stabilen Kollisionsschemen, JSV‑bezogen) citeturn4search9turn4search1  
fan455_piano_synthesis (FE‑Ansätze, Forschungsrepo) citeturn4search30  
Physical‑Modeling‑Piano‑Synthesis (Matlab‑Projekt, 3‑Stufen: Hammer/Strang/Soundboard) citeturn4search26turn4search38

### Daten/Referenzen (Messwerte, IRs, Modaldaten)

**Room/Space IRs (für Realtime‑Faltung):** OpenAIR ist eine etablierte, frei zugängliche IR‑Bibliothek (inkl. verschiedener Formate und Samplingraten). citeturn22search3turn22search18turn22search39

**Resonanzboden‑Messliteratur (für Parameterziele):**  
Mechanische Impedanz/Mobilität wurde experimentell untersucht (z.B. Giordano 1998, sowie Verweise auf Wogram/Conklin‑Messungen). citeturn2search9turn21view3turn2search2turn2search12  
Ege et al. liefern Modalparameter bis 3 kHz und zeigen die lineare Dominanz; Ege/Boutillon liefern Mobilitätssynthese bis 2.5 kHz mit wenigen Parametern. citeturn8view0turn21view2turn21view3

**Unisono‑Kopplung (Zielphänomene):**  
Aramaki et al. fokussieren Beats + double decay als zu reproduzierende Wahrnehmungsphänomene. citeturn8view3turn6search19

### Validierungsexperimente (realismusorientiert, implementierungsnah)

Ohne riesiges Messlabor lassen sich viele Realismuskriterien dennoch systematisch testen:

Attack‑Validierung: Kontaktkraftverlauf (Dauer, Peak‑Form) und resultierende spektrale Helligkeit vs. Anschlagstärke; Power‑Law-/hysteretische Ansätze sind hierfür Standard. citeturn19search3turn17view0turn23view3

Partial‑Tracking: Frequenzen und Abklingzeiten der ersten ~10–20 Teiltöne (inkl. Inharmonizitätstrend); Bensa et al. nutzen genau solche Vergleiche (u.a. Spektrogramme) zur Gegenüberstellung von FDTD und DWG. citeturn7view1turn9view0

Unisono‑Tests: Schwebungsrate und Double‑Decay in 2‑/3‑Saiten‑Kursen, inkl. Brückenkopplung (Waveguide‑Kopplung als effizienter Referenzansatz). citeturn8view3turn6search19turn6search4

Soundboard‑Plausibilität: Modal‑Dichte/Dämpfung bis ~3 kHz, Regimewechsel um ~1 kHz (Rippen‑Localization). citeturn8view0turn25search2

„Black‑Box“-Brückenmobilität: Mobilitätskurve (Mittelwert + Hüllkurve) synthetisiert aus \(n(f),\eta(f),M\) mit Soll‑Messkurven abgleichen. citeturn21view2turn21view3

### Roadmap als Timeline-Flowchart (Implementierungsschritte)

```mermaid
flowchart TD
  A[Woche 1–2: Referenzdaten sammeln<br/>1–2 reale Klaviernoten aufnehmen<br/>Partialtracking + Abklingzeiten] --> B[Woche 3–5: String-Kern<br/>Modale Saite (Transversal)<br/>+ Inharmonizität + freq.-Dämpfung]
  B --> C[Woche 6–8: Hammerkontakt stabil<br/>Power-Law → Hunt–Crossley/Hysterese<br/>Energy-/Passivity-Checks]
  C --> D[Woche 9–11: Unisono-Kurse<br/>2–3 Saiten pro Ton<br/>Brückenkopplung + Detuning]
  D --> E[Woche 12–14: Soundboard linear<br/>Option 1: FIR/FFT-Faltung<br/>Option 2: reduzierte Moden]
  E --> F[Woche 15–17: Abstrahlung & Raum<br/>Strahlungsfilter + OpenAIR-IR]
  F --> G[Woche 18–22: Optimierung & Produktisierung<br/>SIMD/Vectorisierung, Voice-Management<br/>C++/JUCE Plugin, Tests]
  G --> H[Langfristig: High-Fidelity Track<br/>FDTD+Plate gekoppelt + MOR/GPU<br/>Benchmark gegen globales Modell]
```

**Wichtigste explizite Trade-offs (Realismus ↔ CPU/Latenz/Speicher):**

- Realismus steigt stark mit stabilem Kontakt + realistischer Brückenlast; genau hier sind energie‑stabile/Passivitäts‑Formulierungen entscheidend, sonst „numerische Artefakte“. citeturn7view3turn3search0turn23view3
- Soundboard kann (aufgrund Linearität) sehr effizient als Filter/Reduktionsmodell implementiert werden – hoher Realismus pro CPU. citeturn8view0turn20view0
- Vollständige Luftfeldsimulation ist der teuerste Schritt; selbst umfassende globale Modelle betonen Parallelrechnen für 3D‑Felder. citeturn16view2
