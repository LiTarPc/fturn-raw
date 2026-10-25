import {act,fireEvent,render,screen} from '@testing-library/react';
import {afterEach,beforeEach,describe,expect,it,vi} from 'vitest';
import App from './App';
import {api} from './api';
import type {Snapshot} from './types';
import {LogsScreen} from './components/LogsScreen';
vi.mock('./api',()=>({native:true,api:{GetSnapshot:vi.fn(),UpdateRUCIDR:vi.fn(),Disconnect:vi.fn(),Connect:vi.fn()}}));
function snap(revision:number,state='connected'):Snapshot{
 const profile={UplinkMode:'packet',Transport:'udp',ObfProfile:'rtpopus2',Server:'192.0.2.1:56010',VkLink:'https://vk.ru/call/join/demo',Key:'ab'.repeat(32),Mtu:1420,Streams:2,StreamsPerCred:2,RouteMode:'full'} as const;
 return {revision,logError:'',authRetryAt:0,bypass:{settings:{enabled:false,ru:false,sites:[],apps:[]},ruCount:1,ruUpdated:'test',error:''},bypassActive:false,dataDir:'test',elapsedSeconds:0,settings:{minimizeToTray:true,startWithWindows:false,autoConnect:false},trayReady:true,profile,profiles:[{id:'test',name:'test',profile}],activeId:'test',state,detail:'',underlay:'Test adapter',ready:2,logs:[],rx:0,tx:0};
}
function deferred<T>(){let resolve!:(value:T)=>void, reject!:(reason:Error)=>void;const promise=new Promise<T>((yes,no)=>{resolve=yes;reject=no});return {promise,resolve,reject}}
let event:(s:Snapshot)=>void;
const off=vi.fn();
beforeEach(()=>{
 vi.useFakeTimers();vi.clearAllMocks();
 window.runtime={EventsOn:vi.fn((_name,callback)=>{event=callback;return off})};
 vi.mocked(api.GetSnapshot).mockResolvedValue(snap(1));
});
afterEach(()=>{vi.useRealTimers();delete window.runtime});
async function mount(){await act(async()=>{render(<App/>)});}
describe('snapshot ordering',()=>{
 it.each([['idle','connected','Отключить'],['connected','idle','Подключить']])('ignores late %s after %s',async(oldState,newState,button)=>{
  const pending=deferred<Snapshot>();vi.mocked(api.GetSnapshot).mockReturnValueOnce(pending.promise);
  await mount();await act(async()=>event(snap(2,newState)));await act(async()=>pending.resolve(snap(1,oldState)));
  expect(screen.getByRole('button',{name:button})).toBeEnabled();
  expect(screen.queryByRole('button',{name:button==='Отключить'?'Подключить':'Отключить'})).toBeNull();
 });
 it('ignores old events after disconnect and reconnect',async()=>{
  await mount();
  await act(async()=>event(snap(2,'idle')));
  await act(async()=>event(snap(3,'connected')));
  await act(async()=>event(snap(2,'idle')));
  expect(screen.getByRole('button',{name:'Отключить'})).toBeEnabled();
 });
 it('does not overlap polls and detaches events on unmount',async()=>{
  const pending=deferred<Snapshot>();vi.mocked(api.GetSnapshot).mockReturnValueOnce(pending.promise);
  let view!:ReturnType<typeof render>;await act(async()=>{view=render(<App/>)});
  await act(async()=>vi.advanceTimersByTime(5000));expect(api.GetSnapshot).toHaveBeenCalledTimes(1);
  view.unmount();await act(async()=>pending.resolve(snap(1)));await act(async()=>vi.advanceTimersByTime(5000));
  expect(api.GetSnapshot).toHaveBeenCalledTimes(1);expect(off).toHaveBeenCalledTimes(1);
 });
});
describe('disconnect during CIDR update',()=>{
 it.each(['success','failure'])('keeps disconnect available during background %s',async(outcome)=>{
  const update=deferred<void>(),disconnect=deferred<void>();
  vi.mocked(api.UpdateRUCIDR).mockReturnValue(update.promise);vi.mocked(api.Disconnect).mockReturnValue(disconnect.promise);
  await mount();fireEvent.click(screen.getByRole('button',{name:'обход'}));
  fireEvent.click(screen.getByRole('button',{name:'Обновить RU CIDR'}));fireEvent.click(screen.getByRole('button',{name:'vpn'}));
  const power=screen.getByRole('button',{name:'Отключить'});expect(power).toBeEnabled();fireEvent.click(power);fireEvent.click(power);
  expect(api.Disconnect).toHaveBeenCalledTimes(1);expect(power).toBeDisabled();
  vi.mocked(api.GetSnapshot).mockResolvedValue(snap(3,'idle'));
  await act(async()=>disconnect.resolve());expect(screen.getByRole('button',{name:'Подключить'})).toBeDisabled();
  // A stale result accompanying completion of the background command cannot restore connected.
  vi.mocked(api.GetSnapshot).mockResolvedValue(snap(2,'connected'));
  await act(async()=>{if(outcome==='success')update.resolve();else update.reject(new Error('CIDR unavailable'))});
  expect(screen.getByRole('button',{name:'Подключить'})).toBeEnabled();
  if(outcome==='failure')expect(screen.getByRole('status')).toHaveTextContent('CIDR unavailable');
 });
});
describe('logs',()=>{
 it.each(['udp','tcp',''])('shows transport %s',transport=>{
  const value=snap(1);value.profile.Transport=transport as Snapshot['profile']['Transport'];render(<LogsScreen snapshot={value}/>);
  expect(screen.getByText(new RegExp('Test adapter.*'+(transport||'tcp').toUpperCase()))).toBeInTheDocument();
 });
 it('labels idle transport as a profile and exposes disk errors',()=>{
  const value=snap(1,'idle');value.logError='disk unavailable';render(<LogsScreen snapshot={value}/>);
  expect(screen.getByText(/UDP.*профиль/)).toBeInTheDocument();expect(screen.getByRole('alert')).toHaveTextContent('disk unavailable');
 });
});
