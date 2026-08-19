import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AuthGuard } from '../components/AuthGuard';

const state = vi.hoisted(() => ({
  token: 'token',
  query: {
    data: undefined as undefined | { user: { username: string } },
    isLoading: false,
    isError: false,
    refetch: vi.fn()
  }
}));

vi.mock('../services/http', () => ({ getToken: () => state.token }));
vi.mock('../hooks/useIdentity', () => ({ useIdentity: () => state.query }));

describe('auth guard', () => {
  beforeEach(() => {
    state.token = 'token';
    state.query.data = { user: { username: 'admin' } };
    state.query.isLoading = false;
    state.query.isError = false;
  });

  it('redirects anonymous users to login', () => {
    state.token = '';
    render(
      <MemoryRouter initialEntries={['/overview']}>
        <Routes>
          <Route path="/overview" element={<AuthGuard><div>private</div></AuthGuard>} />
          <Route path="/login" element={<div>login</div>} />
        </Routes>
      </MemoryRouter>
    );
    expect(screen.getByText('login')).toBeInTheDocument();
  });

  it('renders authenticated content', () => {
    render(
      <MemoryRouter initialEntries={['/overview']}>
        <Routes>
          <Route path="/overview" element={<AuthGuard><div>private</div></AuthGuard>} />
        </Routes>
      </MemoryRouter>
    );
    expect(screen.getByText('private')).toBeInTheDocument();
  });
});
