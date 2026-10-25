import {useLayoutEffect,useMemo,useRef,useState} from 'react';
import type {Snapshot} from '../api';
import {logLevelLabels,matchesLogFilter,parseLogEntry} from '../logs';
import type {LogFilter} from '../logs';

const filters:{id:LogFilter;label:string}[]=[{id:'all',label:'Все'},{id:'info',label:'Инфо'},{id:'error',label:'Ошибки'}];
export function LogsScreen({snapshot:s}:{snapshot?:Snapshot}){
 const [filter,setFilter]=useState<LogFilter>('all');
 const root=useRef<HTMLDivElement>(null),followTail=useRef(true);
 const entries=useMemo(()=>(s?.logs??[]).map(parseLogEntry),[s?.logs]);
 const visible=useMemo(()=>entries.filter(entry=>matchesLogFilter(entry,filter)),[entries,filter]);
 const lastLine=visible.at(-1)?.line;
 useLayoutEffect(()=>{
  if(root.current&&followTail.current)root.current.scrollTop=root.current.scrollHeight;
 },[filter,lastLine,visible.length]);
 const chooseFilter=(next:LogFilter)=>{followTail.current=true;setFilter(next)};
 return <section className="screen logs-screen">
  <h1>Журнал</h1>
  <p className="hint logs-context">{s?.underlay||'Соединение ещё не выбрано'} · {(s?.profile.Transport||'tcp').toUpperCase()}{s&&['connecting','connected','reconnecting','stopping'].includes(s.state)?'':' (профиль)'}</p>
  {s?.logError&&<p className="notice error" role="alert">Не удалось записать журнал: {s.logError}</p>}
  <div className="log-filters" role="group" aria-label="Фильтр журнала">
   {filters.map(item=><button key={item.id} type="button" className={`log-filter${filter===item.id?' active':''}`} aria-pressed={filter===item.id}
    onClick={()=>chooseFilter(item.id)}>{item.label}<span className="log-filter-count">{entries.filter(entry=>matchesLogFilter(entry,item.id)).length}</span></button>)}
  </div>
  <p className="hint log-legend">INFO — события · WARN — предупреждения · ERROR — ошибки</p>
  <div className="logs" ref={root} aria-label="Записи журнала" onScroll={()=>{
   const el=root.current;if(el)followTail.current=el.scrollHeight-el.scrollTop-el.clientHeight<32;
  }}>
   {visible.length?visible.map(entry=><div className={`log-line log-${entry.level}`} key={`${entry.index}-${entry.line}`}>
    <span className="log-level">{logLevelLabels[entry.level]}</span><span className="log-message">{entry.line}</span>
   </div>):<p className="logs-empty">{entries.length?'Записей для этого фильтра пока нет.':'Записей пока нет.'}</p>}
  </div>
  <p className="hint logs-footer">В «Ошибки» входят WARN и ERROR. Полный журнал: <span className="data-path">{s?.dataDir??'%LOCALAPPDATA%\\fturn-raw'}\ui.log</span></p>
 </section>
}