import {useEffect,useRef} from 'react';
import type {Snapshot} from '../api';
export function LogsScreen({snapshot:s}:{snapshot?:Snapshot}){
 const root=useRef<HTMLDivElement>(null);
 useEffect(()=>{if(root.current)root.current.scrollTop=root.current.scrollHeight},[s?.logs]);
 return <section className="screen"><h1>Журнал</h1><p className="hint">{s?.underlay||'Соединение ещё не выбрано'} · TCP</p><div className="logs" ref={root}>{s?.logs.length?s.logs.map((line,i)=><div className="log-line" key={i}>{line}</div>):'Записей пока нет.'}</div><p className="hint">Журнал сохранён в {s?.dataDir??"%LOCALAPPDATA%\\fturn-raw"}\ui.log.</p></section>
}
