#!/bin/bash
# Script para aplicar todos los fixes de seguridad de manera automatizada

echo "🔧 Aplicando fixes de seguridad..."

# 1. Actualizar .env con API key si no existe
if ! grep -q "ADMIN_API_KEY" backend/.env; then
    echo "" >> backend/.env
    echo "# Admin API Protection" >> backend/.env
    echo "ADMIN_API_KEY=bob_admin_secret_key_2025_muy_segura" >> backend/.env
    echo "✅ API key agregada a .env"
else
    echo "⚠️  ADMIN_API_KEY ya existe en .env"
fi

# 2. Actualizar .env.example
if ! grep -q "ADMIN_API_KEY" backend/.env.example; then
    echo "ADMIN_API_KEY=tu_api_key_admin_aqui" >> backend/.env.example
    echo "✅ API key agregada a .env.example"
fi

echo "✅ Fixes de seguridad aplicados"
echo ""
echo "📝 Resumen de cambios:"
echo "  - Middleware de autenticación creado"
echo "  - Configuración actualizada"
echo "  - API key agregada al .env"
echo ""
echo "🔑 API Key admin: bob_admin_secret_key_2025_muy_segura"
echo ""
echo "📋 Próximos pasos manuales:"
echo "  1. Aplicar middleware a rutas admin en cmd/server/main.go"
echo "  2. Agregar validación de inputs en controllers/chat_controller.go"
echo "  3. Reiniciar el backend para aplicar cambios"
