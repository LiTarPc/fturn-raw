import {IconPlugConnected,IconPlus,IconPower,IconLoader2} from '@tabler/icons-react';
import {native} from '../api';
import type {Snapshot,SavedProfile} from '../api';
import {ProfilePicker} from './ProfilePicker';
import {bytes,duration} from '../format';
const labels:Record<string,string>={idle:'Отключён',connecting:'Подключение…',connected:'Подключён',reconnecting:'Переподключение…',stopping:'Отключение…',error:'Ошибка подключения'};
type Props={snapshot?:Snapshot;busy:boolean;running:boolean;onToggle:()=>void;onAdd:()=>void;onEdit:(p:SavedProfile)=>void;onSelect:(id:string)=>void;onDelete:(id:string)=>void};
export function ConnectionScreen({snapshot:s,busy,running,onToggle,onAdd,onEdit,onSelect,onDelete}:Props){
 const waiting=s?.state==='connecting'||s?.state==='reconnecting';
 return <main className="main">
  <div className="header-bar"><div className="brand-title"><IconPlugConnected size={20}/><span>fturn Raw</span></div><div className="header-actions"><button className="btn-add" disabled={busy} title="Добавить профиль" aria-label="Добавить профиль" onClick={onAdd}><IconPlus size={21}/></button></div></div>
  <div className="center-area">
   <button className={`power-btn${s?.state==='connected'?' power-btn--active':''}${waiting?' power-btn--spinning':''}`} disabled={busy||s?.state==='stopping'||!s} onClick={onToggle} title={running?'Отключить':'Подключить'} aria-label={running?'Отключить':'Подключить'}>{waiting?<IconLoader2 size={48} className="power-icon--spinning"/>:<IconPower size={48}/>}</button>
   <div className="connection-status"><span className={`tunnel-label${s?.state==='error'?' error':''}`}>{labels[s?.state??'idle']}</span><time className="connection-timer" aria-label="Время активного подключения">{duration(s?.elapsedSeconds??0)}</time></div>
   <p className="connection-detail">{s?.detail??'Загрузка профиля…'}{running&&<><br/>Потоки: {s?.ready??0}/{s?.profile.Streams??0}</>}</p>
   <div className="stats-card"><div className="stats-col"><span className="stats-label">Принято</span><span className="stats-value">{bytes(s?.rx??0)}</span></div><div className="stats-divider"/><div className="stats-col"><span className="stats-label">Отправлено</span><span className="stats-value">{bytes(s?.tx??0)}</span></div></div>
  </div>
  <div className="status-bar">
   <ProfilePicker profiles={s?.profiles??[]} activeId={s?.activeId??''} disabled={running||busy} onSelect={onSelect} onEdit={onEdit} onDelete={onDelete}/>
   <p className="profile-caption">{s?.profile.RouteMode==='full'?'Весь IPv4 через Raw':'Только соединение с сервером'} · MTU {s?.profile.Mtu??1420}<br/>Внешнее соединение: {s?.underlay||'автоматически'}<br/>{s?.profile.RouteMode==='full'?'При подключении: IPv6 блокируется, DNS идёт через Raw.':'IPv6 и DNS работают в текущем режиме.'}</p>
   {!native&&<p className="hint">Предпросмотр интерфейса. Подключение доступно в приложении Windows.</p>}
  </div>
 </main>
}
