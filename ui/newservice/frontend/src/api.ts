import type {Backend} from './types';
import {previewApi} from './previewApi';
export type {Profile,SavedProfile,Snapshot,AppSettings} from './types';
declare global {interface Window {go?:{backend:{App:Backend}};runtime?:{EventsOn(name:string,callback:(...args:any[])=>void):()=>void}}}
export const native=Boolean(window.go);
export const api:Backend=window.go?.backend.App??previewApi;
