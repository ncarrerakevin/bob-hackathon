// import { messages } from "../assets/data/mock";
import { useState, useRef, useEffect } from "react";

export default function ChatPanel() {
  const [messages, setMessages] = useState([
    {
      id: Date.now(),
      from: "agent",
      text:
        "¡Hola! Soy tu asistente de BOB Subastas. ¿En qué puedo ayudarte hoy?",
      time: new Date().toISOString(),
    },
  ]);
  const [inputMessage, setInputMessage] = useState("");
  const [isLoading, setIsLoading] = useState(false);
  const [sessionId, setSessionId] = useState(null);
  const [leadScore, setLeadScore] = useState(0);
  const [category, setCategory] = useState("cold");
  const messagesEndRef = useRef(null);

  const scrollToBottom = () => {
    messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
  };

  useEffect(() => {
    scrollToBottom();
  }, [messages]);

  const sendMessage = async () => {
    if (!inputMessage.trim() || isLoading) return;

    const userMessage = inputMessage.trim();
    setInputMessage("");
    setIsLoading(true);

    // Agregar mensaje del usuario
    const newUserMessage = {
      id: Date.now() + Math.random(),
      from: "customer",
      text: userMessage,
      time: new Date().toISOString(),
    };
    setMessages((prev) => [...prev, newUserMessage]);

    try {
      const response = await fetch("/api/chat/message", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          sessionId,
          message: userMessage,
          channel: "web",
        }),
      });

      const data = await response.json();

      if (data.success) {
        // Guardar sessionId si es nuevo
        if (!sessionId) {
          setSessionId(data.sessionId);
        }

        // Actualizar score
        setLeadScore(data.leadScore || 0);
        setCategory(data.category || "cold");

        // Agregar respuesta del asistente
        const assistantMessage = {
          id: Date.now() + Math.random(),
          from: "agent",
          text: data.reply,
          time: data.timestamp,
        };
        setMessages((prev) => [...prev, assistantMessage]);
      } else {
        throw new Error(data.error || "Error desconocido");
      }
    } catch (error) {
      console.error("Error sending message:", error);
      const errorMessage = {
        id: Date.now() + Math.random(),
        from: "agent",
        text:
          "Lo siento, hubo un error al procesar tu mensaje. Por favor intenta de nuevo.",
        time: new Date().toISOString(),
        isError: true,
      };
      setMessages((prev) => [...prev, errorMessage]);
    } finally {
      setIsLoading(false);
    }
  };

  const handleKeyPress = (e) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      sendMessage();
    }
  };

  const getCategoryColor = (category) => {
    switch (category) {
      case 'hot': return '#f56565'
      case 'warm': return '#ed8936'
      case 'cold': return '#4299e1'
      default: return '#cbd5e0'
    }
  }

  return (
    <section className="chat">
      <header className="chat__header">
        <div className="row">
          <div className="presence">
            <div
              className="avatar"
              style={{
                width: 40,
                height: 40,
                backgroundImage:
                  "url(https://lh3.googleusercontent.com/aida-public/AB6AXuBcgCabT-20I_XMduSiPBgxzi8hbas8s_nATFBZvrdowpIP2JBtQGTLcuFtX53Lv0gIkufi3ZeFzya0psgKceSO7w1RERh5I-pLHcWd4n-uf9ue4SH1IT9Im0odyumDyoFvN64JHaSsyQcnRSqoMRz37jFIVgx-FeyFq0eYFFaNtwbRu1cnU-ollZWX9K7vdWrSkspbdMqGE8p1z9W6_NDQA2aRkFlzgmWd_yD3dlkhKohOkKBMrX1yvydlkNVV3bhyQEbOciOucCHw)",
              }}
            />
            <span className="status"></span>
          </div>
          <div className="col">
            <h2 className="title" style={{ margin: 0 }}>
              John Doe
            </h2>
            <div className="row" style={{ gap: 8 }}>
              <span className="chip status" style={{ backgroundColor: getCategoryColor(category), color: '#fff'}}>{category} - {leadScore}</span>
            </div>
          </div>
        </div>

        <div className="row" style={{ gap: 8 }}>
          <button className="icon-btn">
            <span className="material-symbols-outlined">call</span>
          </button>
          <button className="icon-btn">
            <span className="material-symbols-outlined">more_vert</span>
          </button>
        </div>
      </header>

      <div className="messages">
        {messages.map((m) => {
          if (m.from === "system") {
            return (
              <div key={m.id || m.time} className="system">
                <span className="system-chip">{m.text}</span>
              </div>
            );
          }
          return (
            <div
              key={m.id || m.time}
              className={`msg-row ${m.from === "customer" ? "start" : "end"}`}
            >
              {m.from === "customer" && (
                <div
                  className="avatar"
                  style={{
                    width: 32,
                    height: 32,
                    backgroundImage:
                      "url(https://lh3.googleusercontent.com/aida-public/AB6AXuAGEQKBdu5ny63odCrFhox4KsFPf7KH22D2iFW21N_iXFVJSqTCMZlHFgAAw6n7_ORJBhNtscm-iMP4k8YfJPpojYz9dtYLhEOnYtsz20GOB-j9XQ9QgzVUlGOgBPOsiGlAtC7bxORbJYodYv3ptEFZ2dy3p3FlQLJqPgSYnOfQNZHKq8NZsSjKxo2cO1NLAq7pPZjBlfcI8Q1D-IoPZoVM6brxQiKS-9j757eRPfX8-NXqtfURz5PKHPOSaQnQiGI5tWcPqYXNUMce",
                  }}
                />
              )}
              <div
                className={`bubble ${
                  m.from === "customer" ? "customer" : "agent"
                }`}
              >
                <p style={{ margin: 0 }}>{m.text}</p>
                {m.time && <time>{m.time}</time>}
              </div>
            </div>
          );
        })}
        <div ref={messagesEndRef} />
      </div>

      <footer className="composer">
        <div className="composer__inner">
          <input
            placeholder="Type a message..."
            value={inputMessage}
            onChange={e => setInputMessage(e.target.value)}
            onKeyDown={handleKeyPress}
            disabled={isLoading}
          />
          <button className="icon-btn" type="button">
            <span className="material-symbols-outlined">
              sentiment_satisfied
            </span>
          </button>
          <button className="icon-btn" type="button">
            <span className="material-symbols-outlined">attach_file</span>
          </button>
          <button
            className="icon-btn primary"
            type="button"
            onClick={sendMessage}
            disabled={isLoading || !inputMessage.trim()}
          >
            <span className="material-symbols-outlined">send</span>
          </button>
        </div>
      </footer>
    </section>
  );
}
