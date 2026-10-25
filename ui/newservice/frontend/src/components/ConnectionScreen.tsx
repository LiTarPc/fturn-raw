import {IconPlugConnected,IconPlus,IconPower,IconLoader2} from '@tabler/icons-react';
import type {Snapshot,SavedProfile} from '../api';
import {ProfilePicker} from './ProfilePicker';
import {bytes,duration} from '../format';
const labels:Record<string,string>={idle:'Отключён',connecting:'Подключение…',connected:'Подключён',reconnecting:'Переподключение…',stopping:'Отключение…',error:'Ошибка подключения'};
type Props={snapshot?:Snapshot;busy:boolean;connectionBusy:boolean;running:boolean;onToggle:()=>void;onAdd:()=>void;onEdit:(p:SavedProfile)=>void;onSelect:(id:string)=>void;onDelete:(id:string)=>void};
export function ConnectionScreen({snapshot:s,busy,connectionBusy,running,onToggle,onAdd,onEdit,onSelect,onDelete}:Props){
 const waiting=s?.state==='connecting'||s?.state==='reconnecting';
 const authPaused=(s?.authRetryAt??0)>Date.now()/1000;
 const retryTime=new Date((s?.authRetryAt??0)*1000).toLocaleTimeString('ru-RU');
 return <main className="main">
  <div className="header-bar"><div className="brand-title"><IconPlugConnected size={20}/><span>fturn Raw</span></div><div className="header-actions"><button className="btn-add" disabled={busy} title="Добавить профиль" aria-label="Добавить профиль" onClick={onAdd}><IconPlus size={21}/></button></div></div>
  <div className="center-area">
   <button className={`power-btn${s?.state==='connected'?' power-btn--active':''}${waiting?' power-btn--spinning':''}`} disabled={connectionBusy||(!running&&busy)||s?.state==='stopping'||!s||(!running&&authPaused)} onClick={onToggle} title={running?'Отключить':'Подключить'} aria-label={running?'Отключить':'Подключить'}>{waiting?<IconLoader2 size={48} className="power-icon--spinning"/>:<IconPower size={48}/>}</button>
   <div className="connection-status"><span className={`tunnel-label${s?.state==='error'?' error':''}`}>{labels[s?.state??'idle']}</span><time className="connection-timer" aria-label="Время активного подключения">{duration(s?.elapsedSeconds??0)}</time></div>
   {s?.state!=='connected'&&<p className="connection-detail">{s?.detail??'Загрузка профиля…'}</p>}
   {running&&<p className="connection-detail">Потоки: {s?.ready??0}/{s?.profile.Streams??0}</p>}
   {authPaused&&<p className="connection-detail" role="status">VK ограничил попытки капчи. Пауза клиента до {retryTime}. Ограничение VK может действовать дольше.</p>}
   <div className="stats-card"><div className="stats-col"><span className="stats-label">Принято</span><span className="stats-value">{bytes(s?.rx??0)}</span></div><div className="stats-divider"/><div className="stats-col"><span className="stats-label">Отправлено</span><span className="stats-value">{bytes(s?.tx??0)}</span></div></div>
  </div>
  <div className="status-bar">
   <ProfilePicker profiles={s?.profiles??[]} activeId={s?.activeId??''} disabled={running||busy} busy={busy} onSelect={onSelect} onEdit={onEdit} onDelete={onDelete}/>
  </div>
 </main>
}
