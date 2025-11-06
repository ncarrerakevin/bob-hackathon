#!/bin/bash

# Script para iniciar el sistema completo BOB Chatbot

echo "🚀 Iniciando BOB Chatbot - Sistema Multiagente"
echo "================================================"

# Función para matar procesos en puertos específicos
cleanup_ports() {
    echo ""
    echo "🧹 Limpiando puertos anteriores..."
    lsof -ti:3000 | xargs kill -9 2>/dev/null
    lsof -ti:5173 | xargs kill -9 2>/dev/null
    echo "✅ Puertos liberados"
}

# Función para verificar si Go está instalado
check_go() {
    if ! command -v go &> /dev/null; then
        echo "❌ Go no está instalado. Instálalo desde https://go.dev/dl/"
        exit 1
    fi
    echo "✅ Go detectado: $(go version)"
}

# Función para verificar si Node está instalado
check_node() {
    if ! command -v node &> /dev/null; then
        echo "❌ Node.js no está instalado. Instálalo desde https://nodejs.org/"
        exit 1
    fi
    echo "✅ Node.js detectado: $(node --version)"
    echo "✅ npm detectado: $(npm --version)"
}

# Función para verificar archivos .env
check_env() {
    echo ""
    echo "🔍 Verificando archivos de configuración..."

    if [ ! -f "backend/.env" ]; then
        echo "❌ backend/.env no existe"
        echo "💡 Copia backend/.env.example a backend/.env y configura tu API key"
        exit 1
    fi

    if [ ! -f "frontend/.env" ]; then
        echo "⚠️  frontend/.env no existe, creando desde .env.example..."
        cp frontend/.env.example frontend/.env 2>/dev/null || echo "ℹ️  No hay .env.example en frontend"
    fi

    echo "✅ Archivos de configuración OK"
}

# Función para instalar dependencias
install_dependencies() {
    echo ""
    echo "📦 Instalando dependencias..."

    # Backend Go
    echo "📦 Backend Go dependencies..."
    cd backend
    go mod tidy
    cd ..

    # Frontend React (solo si existe package.json)
    if [ -f "frontend/package.json" ]; then
        echo "📦 Frontend React dependencies..."
        cd frontend
        npm install --silent
        cd ..
    fi

    echo "✅ Dependencias instaladas"
}

# Función para iniciar backend
start_backend() {
    echo ""
    echo "🔧 Iniciando Backend Go (puerto 3000)..."
    cd backend
    go run cmd/server/main.go &
    BACKEND_PID=$!
    cd ..
    echo "✅ Backend iniciado (PID: $BACKEND_PID)"
    sleep 3
}

# Función para iniciar frontend
start_frontend() {
    if [ -f "frontend/package.json" ]; then
        echo ""
        echo "🎨 Iniciando Frontend React (puerto 5173)..."
        cd frontend
        npm run dev &
        FRONTEND_PID=$!
        cd ..
        echo "✅ Frontend iniciado (PID: $FRONTEND_PID)"
        sleep 2
    else
        echo "ℹ️  No hay frontend configurado, solo backend corriendo"
    fi
}

# Función para verificar que los servicios estén corriendo
verify_services() {
    echo ""
    echo "🔍 Verificando servicios..."

    # Verificar backend
    sleep 2
    if curl -s http://localhost:3000/health > /dev/null 2>&1; then
        echo "✅ Backend OK: http://localhost:3000"
    else
        echo "❌ Backend no responde en http://localhost:3000"
        echo "⚠️  Verifica los logs arriba"
    fi

    # Verificar frontend si existe
    if [ -f "frontend/package.json" ]; then
        if curl -s http://localhost:5173 > /dev/null 2>&1; then
            echo "✅ Frontend OK: http://localhost:5173"
        else
            echo "⚠️  Frontend puede tardar unos segundos en iniciar..."
        fi
    fi
}

# Función para mostrar información final
show_info() {
    echo ""
    echo "================================================"
    echo "✅ SISTEMA BOB CHATBOT INICIADO"
    echo "================================================"
    echo ""
    echo "📍 Endpoints disponibles:"
    echo "   Backend:  http://localhost:3000"
    echo "   Health:   http://localhost:3000/health"
    echo "   API Docs: http://localhost:3000/"
    if [ -f "frontend/package.json" ]; then
        echo "   Frontend: http://localhost:5173"
    fi
    echo ""
    echo "📊 Sistema Multiagente:"
    echo "   Orchestrator: ✅ Activo (spam, routing, intención)"
    echo "   FAQ Agent:    ✅ Activo (preguntas frecuentes)"
    echo "   Auction Agent:✅ Activo (búsqueda vehículos)"
    echo "   Scoring Agent:✅ Activo (7 dimensiones, 0-100 pts)"
    echo ""
    echo "🔌 Endpoint principal para WhatsApp:"
    echo "   POST http://localhost:3000/api/chat/message"
    echo ""
    echo "📝 Para detener todo:"
    echo "   Ctrl+C en esta terminal o ejecuta: ./stop.sh"
    echo ""
    echo "📋 Logs:"
    echo "   Los logs aparecerán debajo de este mensaje..."
    echo "================================================"
}

# MAIN - Ejecución principal
main() {
    # Verificar requisitos
    check_go
    check_node

    # Limpiar puertos
    cleanup_ports

    # Verificar configuración
    check_env

    # Instalar dependencias
    read -p "¿Instalar/actualizar dependencias? (y/N): " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]]; then
        install_dependencies
    fi

    # Iniciar servicios
    start_backend
    start_frontend

    # Verificar que todo esté corriendo
    verify_services

    # Mostrar información
    show_info

    # Mantener el script corriendo
    echo "⏳ Sistema corriendo... (Presiona Ctrl+C para detener)"
    wait
}

# Manejo de Ctrl+C
trap 'echo -e "\n🛑 Deteniendo servicios..."; lsof -ti:3000 | xargs kill -9 2>/dev/null; lsof -ti:5173 | xargs kill -9 2>/dev/null; echo "✅ Servicios detenidos"; exit 0' INT

# Ejecutar script principal
main
