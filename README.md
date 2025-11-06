# bob chatbot - hackathon 2025

sistema de chatbot inteligente con arquitectura multiagente y lead scoring de 7 dimensiones para bob subastas. backend en go con gemini ai 2.5 flash.

## inicio rapido

### opcion 1: script automatico (recomendado)

```bash
./start.sh
```

este script:
- verifica dependencias (go, node.js)
- limpia puertos anteriores
- verifica archivos .env
- inicia backend en puerto 3000
- inicia frontend en puerto 5173
- muestra urls y endpoints disponibles

para detener todo:
```bash
./stop.sh
```

### opcion 2: inicio manual

backend:
```bash
cd backend
go run cmd/server/main.go
```

frontend:
```bash
cd frontend
npm install  # solo primera vez
npm run dev
```

verificar:
```bash
curl http://localhost:3000/health
```

## arquitectura multiagente

el sistema implementa 1 orchestrator + 3 subagentes especializados:

```
usuario → orchestrator agent (routing + spam detection)
              ↓
         ┌────┴────┬────────────┐
         ↓         ↓            ↓
    faq agent  auction agent  scoring agent
    (preguntas) (vehiculos)   (7 dimensiones)
```

### orchestrator agent
- detecta spam y mensajes ambiguos
- clasifica intencion: faq, subasta, general, spam, ambiguo
- rutea a agente especializado segun contexto
- maneja saludos y conversacion general

### faq agent
- busca en base de conocimiento (62+ faqs)
- sintetiza respuestas de multiples faqs relevantes
- tono amigable y profesional

### auction agent
- consulta vehiculos via bob api
- analiza necesidades del usuario
- recomienda vehiculos que coincidan
- hace preguntas de calificacion (urgencia, presupuesto)

### scoring agent
- sistema oficial de 7 dimensiones (0-100 puntos)
- calcula despues de 6+ mensajes en la conversacion
- aplica boosts (+3 a +7) y penalizaciones (-2 a -6)
- clasificacion: hot (85-100), warm (65-84), cold (45-64), discarded (<45)

## sistema de scoring (7 dimensiones)

**dimension 1: perfil demografico (0-10 puntos)**
- ubicacion, profesion, coherencia, contexto

**dimension 2: comportamiento digital (0-15 puntos)**
- velocidad respuesta, nivel detalle, engagement, completitud

**dimension 3: capacidad financiera (0-25 puntos)**
- presupuesto mencionado, autoridad compra, timeframe, experiencia

**dimension 4: necesidad/urgencia (0-15 puntos)**
- nivel urgencia, consecuencias, presion temporal

**dimension 5: experiencia previa (0-10 puntos)**
- experiencia en subastas, compras online

**dimension 6: engagement actual (0-10 puntos)**
- disponibilidad, interes demo, solicitudes especificas

**dimension 7: contexto de compra (0-15 puntos)**
- motivo compra, investigacion realizada, conocimiento producto

**boosts**: referidos (+7), mencion competencia (+6), solicita especialista (+6), fecha especifica (+5), pregunta garantias (+4), conocimiento tecnico (+3)

**penalizaciones**: tire-patadas (-6), inconsistencias (-5), evasivo presupuesto (-4), multiples consultas sin compromiso (-2)

**clasificacion de leads**:
- hot (85-100): contacto inmediato (1h) por especialista, seguimiento 4h
- warm (65-84): contacto 4-8h por especialista, seguimiento 24h
- cold (45-64): invitar a comunidad, seguimiento 1 mes
- discarded (<45): no contactar

## api endpoints

total: 18 endpoints activos

### chat / conversacion
```bash
# enviar mensaje (sistema multiagente)
post /api/chat/message
{
  "message": "busco un auto toyota",
  "channel": "whatsapp",
  "sessionId": "opcional"
}

# calcular scoring
post /api/chat/score
{ "sessionId": "whatsapp-123" }

# ver historial
get /api/chat/history/:sessionId

# eliminar sesion
delete /api/chat/session/:sessionId
```

### leads
```bash
# listar leads
get /api/leads?category=hot&channel=whatsapp

# lead especifico
get /api/leads/:sessionId

# estadisticas (hot/warm/cold)
get /api/leads/stats
```

### recursos
```bash
# faqs
get /api/faqs?search=subasta

# vehiculos
get /api/vehicles?marca=toyota&limit=10

# vehiculo especifico
get /api/vehicles/:id
```

### administracion
```bash
# subir csv de faqs (usuarios no tecnicos pueden actualizar desde excel)
post /api/admin/faqs/upload
content-type: multipart/form-data
body: file=faqs.csv

# descargar template csv
get /api/admin/faqs/template

# descargar faqs actuales como csv
get /api/admin/faqs/download

# obtener prompts de todos los agentes
get /api/admin/prompts

# actualizar prompt de un agente (orchestrator, faq, auction, scoring)
put /api/admin/prompts/:agent
{
  "prompt": "nuevo prompt personalizado aqui"
}
```

### health
```bash
get /health
```

## integracion whatsapp

endpoint principal:
```
post http://localhost:3000/api/chat/message
```

ejemplo payload:
```json
{
  "message": "hola, busco una camioneta para mi empresa",
  "channel": "whatsapp",
  "sessionId": "whatsapp-51987654321"
}
```

ejemplo respuesta:
```json
{
  "success": true,
  "sessionId": "whatsapp-51987654321",
  "reply": "hola, claro que te puedo ayudar...",
  "leadScore": 45,
  "category": "cold",
  "timestamp": "2025-11-05t23:30:53z"
}
```

el sistema multiagente se encarga automaticamente de:
- detectar spam
- rutear a agente correcto (faq/auction)
- calcular scoring progresivo
- clasificar lead

## configuracion

primera vez:
```bash
# backend
cd backend
cp .env.example .env
# editar .env y agregar tu gemini_api_key

# frontend (opcional)
cd frontend
cp .env.example .env
```

archivo backend/.env:
```env
gemini_api_key=tu_api_key_aqui
gemini_model=gemini-2.5-flash
port=3000
bob_api_base_url=https://apiv3.somosbob.com/v3
cors_origins=http://localhost:5173,http://localhost:3000
frontend_url=http://localhost:5173
```

## estructura del proyecto

```
backend/
├── cmd/server/main.go          # servidor principal
├── internal/
│   ├── agents/                 # sistema multiagente
│   │   ├── base.go            # interfaces y tipos base
│   │   ├── orchestrator.go    # routing y spam detection
│   │   ├── faq_agent.go       # preguntas frecuentes
│   │   ├── auction_agent.go   # busqueda vehiculos
│   │   └── scoring_agent.go   # scoring 7 dimensiones
│   ├── config/                # configuracion
│   ├── controllers/           # chat & leads
│   ├── services/              # session, bob api, faqs
│   └── models/                # estructuras de datos
├── data/                      # faqs, vehiculos, sesiones
├── .env                       # configuracion (no en git)
└── go.mod

frontend/
├── src/
│   ├── components/            # chatwidget, dashboard
│   └── app.jsx
└── vite.config.js

start.sh                       # script inicio automatico
stop.sh                        # script detener servicios
test_multiagent.py             # suite de pruebas
```

## testing

script de pruebas automatico:
```bash
python3 test_multiagent.py
```

pruebas que ejecuta:
- faq routing (preguntas sobre proceso)
- auction routing (busqueda vehiculos)
- spam detection
- manejo mensajes ambiguos
- scoring progresivo (6-14 mensajes)
- clasificacion hot/warm/cold

test manual:
```bash
curl -x post http://localhost:3000/api/chat/message \
  -h "content-type: application/json" \
  -d '{"message": "hola", "channel": "web"}'
```

## troubleshooting

backend no inicia:
```bash
lsof -ti:3000 | xargs kill -9
cd backend
go run cmd/server/main.go
```

frontend no inicia:
```bash
lsof -ti:5173 | xargs kill -9
cd frontend
npm run dev
```

gemini no responde:
- verificar api key en backend/.env
- verificar modelo: debe ser gemini-2.5-flash
- verificar conexion a internet

puertos ocupados:
```bash
./stop.sh
./start.sh
```

## stack tecnologico

- go 1.21+ - backend (5-10x mas rapido que node.js)
- gin - framework web
- gemini ai 2.5 flash - ia conversacional
- react + vite - frontend
- arquitectura multiagente - orchestrator + 3 subagentes
- scoring 7 dimensiones - sistema oficial hackathon

## flujo del sistema

1. usuario envia mensaje via whatsapp/web
2. orchestrator analiza intencion y detecta spam
3. si debe rutear: envia a faq agent o auction agent
4. si es general/spam/ambiguo: orchestrator responde directamente
5. despues de 6+ mensajes: scoring agent calcula score
6. sistema clasifica lead (hot/warm/cold/discarded)
7. retorna respuesta + score actualizado

## estado del proyecto

- [x] backend go funcionando
- [x] frontend react completo
- [x] sistema multiagente (orchestrator + 3 subagentes)
- [x] scoring 7 dimensiones oficial
- [x] orchestrator con spam detection
- [x] faq agent especializado
- [x] auction agent especializado
- [x] scoring agent con boosts y penalizaciones
- [x] api bob conectada
- [x] 62 faqs cargadas
- [x] scripts de inicio automatico
- [x] suite de pruebas
- [x] integracion whatsapp ready
- [ ] deploy a produccion

## notas tecnicas

- backend guarda datos en backend/data/*.json
- session ids: web-uuid o whatsapp-numero
- cache bob api: 5 minutos
- faqs y vehiculos se cargan al iniciar
- scoring se calcula despues de 6 mensajes (3 pares user-assistant)
- sistema cross-platform (windows/linux/macos)
- sin hardcoding, todo via .env
- repositorio listo para publico (sin api keys)

---

hackathon bob 2025
