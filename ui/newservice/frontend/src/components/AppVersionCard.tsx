import {useEffect,useState} from 'react';
import {IconCpu,IconRefresh} from '@tabler/icons-react';
import {api,native} from '../api';
import type {VersionInfo,UpdateInfo} from '../types';

type Props={busy:boolean;running:boolean;invoke:(fn:()=>Promise<unknown>)=>Promise<boolean>};
function versionLabel(value?:string){return !value?'Загрузка…':value==='development'?'Сборка разработки':value==='unknown'?'Не определена':value}
export function AppVersionCard({busy,running,invoke}:Props){
 const [versions,setVersions]=useState<VersionInfo>();
 const [update,setUpdate]=useState<UpdateInfo>();
 const [checking,setChecking]=useState(native),[error,setError]=useState('');
 useEffect(()=>{
  let alive=true;
  void api.GetVersionInfo().then(v=>{if(alive)setVersions(v)}).catch(e=>{if(alive)setError(String(e))});
  if(native)void api.CheckForUpdates().then(v=>{if(alive)setUpdate(v)}).catch(e=>{if(alive)setError(String(e))}).finally(()=>{if(alive)setChecking(false)});
  return()=>{alive=false};
 },[]);
 const check=async()=>{
  setChecking(true);setError('');
  try{setUpdate(await api.CheckForUpdates())}catch(e){setError(String(e))}finally{setChecking(false)}
 };
 return <section className="core-card app-version-card" aria-label="Версии и обновления">
  <strong><IconCpu size={17}/> Приложение и ядро</strong>
  <dl className="version-list"><div><dt>fturn Raw</dt><dd>{versionLabel(versions?.appVersion)}</dd></div><div><dt>Ядро Raw</dt><dd>{versionLabel(versions?.coreVersion)}</dd></div></dl>
  <p className="hint" role="status">{checking?'Проверяем обновления…':error||(!native?'Обновления доступны в приложении Windows.':update?.available?`Доступна версия ${update.version}`:update?.currentVersion==='development'?`Последний выпуск: ${update.version}`:update?'Установлена актуальная версия.':'')}</p>
  <button className="action secondary update-check" disabled={busy||checking||!native} onClick={()=>void check()}><IconRefresh size={15}/>{checking?'Проверка…':'Проверить обновления'}</button>
  {update?.available&&<><button className="action" disabled={busy||checking||running} onClick={()=>void invoke(()=>api.InstallUpdate())}>{busy?'Подготовка обновления…':`Обновить до ${update.version}`}</button><p className="hint">{running?'Для установки отключите соединение.':'Приложение закроется и откроет установщик. Обновятся интерфейс и ядро Raw; профили сохранятся.'}</p></>}
 </section>
}