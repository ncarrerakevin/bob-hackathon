#!/bin/bash

# Script para detener el sistema completo BOB Chatbot

echo "🛑 Deteniendo BOB Chatbot - Sistema Multiagente"
echo "================================================"

# Detener backend (puerto 3000)
echo "🔧 Deteniendo Backend (puerto 3000)..."
lsof -ti:3000 | xargs kill -9 2>/dev/null
if [ $? -eq 0 ]; then
    echo "✅ Backend detenido"
else
    echo "ℹ️  Backend no estaba corriendo"
fi

# Detener frontend (puerto 5173)
echo "🎨 Deteniendo Frontend (puerto 5173)..."
lsof -ti:5173 | xargs kill -9 2>/dev/null
if [ $? -eq 0 ]; then
    echo "✅ Frontend detenido"
else
    echo "ℹ️  Frontend no estaba corriendo"
fi

# Limpiar procesos de Go que puedan estar corriendo
pkill -f "go run cmd/server/main.go" 2>/dev/null

# Limpiar procesos de npm/vite
pkill -f "vite" 2>/dev/null
pkill -f "npm run dev" 2>/dev/null

echo ""
echo "✅ Todos los servicios detenidos"
echo "================================================"
