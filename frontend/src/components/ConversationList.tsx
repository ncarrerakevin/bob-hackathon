import React from 'react'
import { conversations } from '../assets/data/mock'

export default function ConversationList() {
  return (
    <main className="conversations">
      <div className="panel-section">
        <label className="search">
          <div className="search__icon"><span className="material-symbols-outlined">search</span></div>
          <input placeholder="Search conversations..." />
        </label>
      </div>

      <div className="panel-section">
        <div className="filters">
          <button className="btn primary">All</button>
          <button className="btn secondary">Unread</button>
          <button className="btn secondary">Hot Leads</button>
        </div>
      </div>

      <div className="convlist">
        {conversations.map((c) => (
          <div key={c.id} className={`conv-item ${c.active ? 'active' : ''}`}>
            <div className="conv-meta">
              <div className="avatar" style={{width:48,height:48, backgroundImage:`url(${c.avatar})`}} />
              <div className="col" style={{gap:4}}>
                <p className="conv-name">{c.name}</p>
                <p className={c.active ? 'text-primary' : 'conv-snippet'}>{c.lastMessage}</p>
              </div>
            </div>
            <div className="col" style={{gap:6, alignItems:'flex-end'}}>
              <p className="subtitle" style={{fontSize:12}}>{c.time}</p>
              {typeof c.unread === 'number' && (
                <div className={`badge ${c.unread && c.unread > 1 && !c.active ? 'red' : ''}`}>{c.unread}</div>
              )}
            </div>
          </div>
        ))}
      </div>
    </main>
  )
}
