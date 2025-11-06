import "./styles/theme.css";
import "./styles/layout.css";
import "./styles/components.css";
import Sidebar from "./components/SideBar";
import { Route, Routes } from "react-router-dom";
import Chat from "./pages/Chat";
import Leads from "./pages/Leads";
import Dashboard from "./pages/Dashboard";

function App() {
  return (
    <div className="app">
      <Sidebar />
      <Routes>
        <Route path="/" element={<Dashboard />} />
        <Route path="/chat" element={<Chat />} />
        <Route path="/leads" element={<Leads />} />
        {/* <Route path="/settings" element={<Settings />} /> */}
      </Routes>
      {/* <footer className="app-footer">
        <p>Hackathon BOB 2025 | Powered by Gemini AI 2.5 Flash</p>
      </footer> */}
    </div>
  );
}

export default App;
