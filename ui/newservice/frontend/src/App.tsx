import {useState} from 'react';
import {api} from './api';
import type {SavedProfile} from './api';
import {useClient} from './hooks/useClient';
import {Sidebar} from './components/Sidebar';
import type {Page} from './components/Sidebar';
import {ConnectionScreen} from './components/ConnectionScreen';
import {ProfileEditor} from './components/ProfileEditor';
import {AppSettingsScreen} from './components/AppSettingsScreen';
import {BypassScreen} from './components/BypassScreen';
import {LogsScreen} from './components/LogsScreen';
export default function App(){
 const client=useClient();
 const {snapshot,busy,notice,setNotice,invoke,running}=client;
 const [page,setPage]=useState<Page>('vpn'),[editing,setEditing]=useState<SavedProfile>();
 const navigate=(p:Page)=>{setNotice('');setPage(p)};
 const edit=(entry:SavedProfile)=>void invoke(async()=>{await api.SelectProfile(entry.id);setEditing(entry);setPage('editor')});
 return <div className="layout"><Sidebar page={page} onNavigate={navigate}/><div className="content">
  {notice&&<div className="notice global-notice" role="status">{notice}</div>}
  {page==='vpn'&&<ConnectionScreen snapshot={snapshot} busy={busy} running={running}
   onToggle={()=>void invoke(()=>running?api.Disconnect():api.Connect())}
   onAdd={()=>{setEditing(undefined);navigate('editor')}} onEdit={edit}
   onSelect={id=>void invoke(()=>api.SelectProfile(id))} onDelete={id=>void invoke(()=>api.DeleteProfile(id))}/>}
  {page==='editor'&&<ProfileEditor key={editing?.id??'new'} entry={editing} busy={busy} running={running} invoke={invoke} setNotice={setNotice} onSaved={()=>navigate('vpn')} onBack={()=>navigate('vpn')}/>}
  {page==='settings'&&<AppSettingsScreen snapshot={snapshot} busy={busy} onChange={s=>void invoke(()=>api.SaveAppSettings(s))} onBack={()=>navigate('vpn')} onExit={()=>void invoke(()=>api.Exit())}/>}
  {page==='bypass'&&<BypassScreen snapshot={snapshot} busy={busy} running={running} invoke={invoke} onBack={()=>navigate('vpn')}/>}
  {page==='logs'&&<LogsScreen snapshot={snapshot}/>}
 </div></div>
}
