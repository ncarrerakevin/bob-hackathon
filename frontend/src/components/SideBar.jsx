import logo from "../assets/images/Logo.jpg";
import { NavLink } from "react-router-dom";

export default function Sidebar() {
  return (
    <aside className="sidebar">
      <div className="sidebar__inner">
        <div className="col" style={{ gap: 16 }}>
          <div className="brand">
            <img
              className="brand__logo"
              src={logo}
              alt="Logo"
              width={120}
              height={120}
            />
            <div className="col">
              <div className="brand__title">BOB Chatbot</div>
              <div className="brand__subtitle">Chat Management</div>
            </div>
          </div>

          <nav className="nav">
            <NavLink to="/" end>
              <span className="material-symbols-outlined">dashboard</span>
              <p>Dashboard</p>
            </NavLink>

            <NavLink to="/chat">
              <span className="material-symbols-outlined">chat_bubble</span>
              <p>Chats</p>
            </NavLink>

            <NavLink to="/leads">
              <span className="material-symbols-outlined">group</span>
              <p>Leads</p>
            </NavLink>

            <NavLink to="/settings">
              <span className="material-symbols-outlined">settings</span>
              <p>Settings</p>
            </NavLink>
          </nav>
        </div>

        <div className="sidebar__profile">
          <div className="profile-row">
            <div
              className="avatar"
              style={{
                backgroundImage:
                  "url(https://lh3.googleusercontent.com/aida-public/AB6AXuAWszltzxgn5yB94l4Bb0S05aTpf7hTS0SpQgZ_1A8sJ2kB5sb8oy2gKR2GatBXxxnZq03dRNt5G7OOfQebic2GkK1LCwbSS2VHhEGRzq7ooXy1hAE2pmNmjIEKQB4d8Ql0Ycn4a-C6VEwFZP-SGuZNEkcRWaCs4qktvJvum8sbq8Se3WwrMbDGlYK0yngFKxAUbsTg19IeJ3kVkcaJvro57ymc8kw7vflY1QSedoH1NOb8lYRO4dy_uSGwrvBazCJqGKU7UXR0Pbt9)",
              }}
            />
            <p className="title">Agent Smith</p>
          </div>
        </div>
      </div>
    </aside>
  );
}
