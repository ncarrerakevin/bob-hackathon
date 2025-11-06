import React, { useState } from "react";
import ConversationList from "../components/ConversationList";
import ChatPanel from "../components/ChatPanel";
import ChatWidget from "../components/ChatWidget";
import LeadsDashboard from "../components/LeadsDashboard";

const Chat = () => {
  return (
    <>
      <ConversationList />
      <ChatPanel />
      {/* <header className="app-header">
        <div className="header-content">
          <h1>🤖 BOB Chatbot</h1>
          <p>Asistente Virtual de Subastas</p>
        </div>
        <nav className="nav-tabs">
          <button
            className={`nav-tab ${activeView === "chat" ? "active" : ""}`}
            onClick={() => setActiveView("chat")}
          >
            💬 Chat
          </button>
          <button
            className={`nav-tab ${activeView === "leads" ? "active" : ""}`}
            onClick={() => setActiveView("leads")}
          >
            📊 Leads
          </button>
        </nav>
      </header> */}
    </>
  );
};

export default Chat;
